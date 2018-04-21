/*
   Multihost
   Multiple ports
*/

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	slog "github.com/olitvin/log"
	"github.com/olitvin/skydock/docker"
	"github.com/olitvin/skydock/utils"
	"github.com/skynetservices/skydns1/client"
	"github.com/skynetservices/skydns1/msg"
)

type Params struct {
	PathToSocket        string
	Domain              string
	Environment         string
	SkydnsUrl           string
	SkydnsContainerName string
	Secret              string
	TTL                 int
	Beat                int
	NumberOfHandlers    int
	PluginFile          string
}

var (
	params Params

	skydns       Skydns
	dockerClient docker.Docker
	plugins      *pluginRuntime
	running      = make(map[string]struct{})
	runningLock  = sync.Mutex{}
)

func init() {
	flag.StringVar(&params.PathToSocket, "s", "/var/run/docker.sock", "path to the docker unix socket")
	flag.StringVar(&params.SkydnsUrl, "skydns", "", "url to the skydns url")
	flag.StringVar(&params.SkydnsContainerName, "name", "", "name of skydns container")
	flag.StringVar(&params.Secret, "secret", "", "skydns secret")
	flag.StringVar(&params.Domain, "domain", "", "same domain passed to skydns")
	flag.StringVar(&params.Environment, "environment", "dev", "environment name where service is running")
	flag.IntVar(&params.TTL, "ttl", 60, "default ttl to use when registering a service")
	flag.IntVar(&params.Beat, "beat", 0, "heartbeat interval")
	flag.IntVar(&params.NumberOfHandlers, "workers", 3, "number of concurrent workers")
	flag.StringVar(&params.PluginFile, "plugins", "/plugins/default.js", "file containing javascript plugins (plugins.js)")
	flag.Parse()

	b, err := json.Marshal(params)
	if err != nil {
		log.Panicln(err)
	}

	log.Println("Start with params: ", string(b))
}

func validateSettings() {
	if params.Beat < 1 {
		params.Beat = params.TTL - (params.TTL / 4)
	}

	if (params.SkydnsUrl != "") && (params.SkydnsContainerName != "") {
		fatal(fmt.Errorf("specify 'name' or 'skydns', not both"))
	}

	if (params.SkydnsUrl == "") && (params.SkydnsContainerName == "") {
		params.SkydnsUrl = "http://" + os.Getenv("SKYDNS_PORT_8080_TCP_ADDR") + ":8080"
	}

	if params.Domain == "" {
		fatal(fmt.Errorf("Must specify your skydns domain"))
	}
}

func setupLogger() error {
	return nil
}

func heartbeat(uuid string) {
	runningLock.Lock()
	if _, exists := running[uuid]; exists {
		runningLock.Unlock()
		return
	}
	running[uuid] = struct{}{}
	runningLock.Unlock()

	defer func() {
		runningLock.Lock()
		delete(running, uuid)
		runningLock.Unlock()
	}()

	var errorCount int
	for _ = range time.Tick(time.Duration(params.Beat) * time.Second) {
		if errorCount > 10 {
			// if we encountered more than 10 errors just quit
			slog.Printf(slog.ERROR, "aborting heartbeat for %s after 10 errors", uuid)
			return
		}

		// don't fill logs if we have a low params.Beat
		// may need to do something better here
		if params.Beat >= 30 {
			slog.Printf(slog.INFO, "updating params.TTL for %s", uuid)
		}

		if err := updateService(uuid, params.TTL); err != nil {
			errorCount++
			slog.Printf(slog.ERROR, "%s", err)
			break
		}
	}
}

// restoreContainers loads all running containers and inserts
// them into skydns when skydock starts
func restoreContainers() error {
	containers, err := dockerClient.FetchAllContainers()
	if err != nil {
		return err
	}

	var container *docker.Container
	for _, cnt := range containers {
		uuid := utils.Truncate(cnt.Id)
		if container, err = dockerClient.FetchContainer(uuid, cnt.Image); err != nil {
			if err != docker.ErrImageNotTagged {
				slog.Printf(slog.ERROR, "failed to fetch %s on restore: %s", cnt.Id, err)
			}
			continue
		}

		service, err := plugins.createService(container)
		if err != nil {
			// doing a fatal here because we cannot do much if the plugins
			// return an invalid service or error
			fatal(err)
		}
		if err := sendService(uuid, service); err != nil {
			slog.Printf(slog.ERROR, "failed to send %s to skydns on restore: %s", uuid, err)
		}
	}
	return nil
}

// sendService sends the uuid and service data to skydns
func sendService(uuid string, service *msg.Service) error {
	slog.Printf(slog.INFO, "adding %s (%s) to skydns", uuid, service.Name)
	if err := skydns.Add(uuid, service); err != nil {
		// ignore erros for conflicting uuids and start the heartbeat again
		if err != client.ErrConflictingUUID {
			return err
		}
		slog.Printf(slog.INFO, "service already exists for %s. Resetting params.TTL.", uuid)
		updateService(uuid, params.TTL)
	}
	go heartbeat(uuid)
	return nil
}

func removeService(uuid string) error {
	slog.Printf(slog.INFO, "removing %s from skydns", uuid)
	return skydns.Delete(uuid)
}

func addService(uuid, image string) error {
	container, err := dockerClient.FetchContainer(uuid, image)
	if err != nil {
		if err != docker.ErrImageNotTagged {
			return err
		}
		return nil
	}

	service, err := plugins.createService(container)
	if err != nil {
		// doing a fatal here because we cannot do much if the plugins
		// return an invalid service or error
		fatal(err)
	}

	if err := sendService(uuid, service); err != nil {
		return err
	}
	return nil
}

func updateService(uuid string, ttl int) error {
	return skydns.Update(uuid, uint32(ttl))
}

func eventHandler(c chan *docker.Event, group *sync.WaitGroup) {
	defer group.Done()

	for event := range c {
		slog.Printf(slog.DEBUG, "received event (%s) %s %s", event.Status, event.ContainerId, event.Image)
		uuid := utils.Truncate(event.ContainerId)

		switch event.Status {
		case "die", "stop", "kill":
			if err := removeService(uuid); err != nil {
				slog.Printf(slog.ERROR, "error removing %s from skydns: %s", uuid, err)
			}
		case "start", "restart":
			if err := addService(uuid, event.Image); err != nil {
				slog.Printf(slog.ERROR, "error adding %s to skydns: %s", uuid, err)
			}
		}
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "%s\n", err)
	os.Exit(1)

}

func main() {
	validateSettings()
	if err := setupLogger(); err != nil {
		fatal(err)
	}

	var (
		err   error
		group = &sync.WaitGroup{}
	)

	plugins, err = newRuntime(params.PluginFile)
	if err != nil {
		fatal(err)
	}

	if dockerClient, err = docker.NewClient(params.PathToSocket); err != nil {
		slog.Printf(slog.FATAL, "error connecting to docker: %s", err)
		fatal(err)
	}

	if params.SkydnsContainerName != "" {
		container, err := dockerClient.FetchContainer(params.SkydnsContainerName, "")
		if err != nil {
			slog.Printf(slog.FATAL, "error retrieving skydns container '%s': %s", params.SkydnsContainerName, err)
			fatal(err)
		}

		params.SkydnsUrl = "http://" + container.NetworkSettings.IpAddress + ":8080"
	}

	slog.Printf(slog.INFO, "skydns URL: %s", params.SkydnsUrl)

	if skydns, err = client.NewClient(params.SkydnsUrl, params.Secret, params.Domain, "172.17.42.1:53"); err != nil {
		slog.Printf(slog.FATAL, "error connecting to skydns: %s", err)
		fatal(err)
	}

	slog.Printf(slog.DEBUG, "starting restore of containers")
	if err := restoreContainers(); err != nil {
		slog.Printf(slog.FATAL, "error restoring containers: %s", err)
		fatal(err)
	}

	events := dockerClient.GetEvents()

	group.Add(params.NumberOfHandlers)
	// Start event handlers
	for i := 0; i < params.NumberOfHandlers; i++ {
		go eventHandler(events, group)
	}

	slog.Printf(slog.DEBUG, "starting main process")
	group.Wait()
	slog.Printf(slog.DEBUG, "stopping cleanly via EOF")
}
