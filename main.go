/*
   Multihost
   Multiple ports
*/

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/olitvin/skydock/docker"
	log "github.com/olitvin/skydock/slog"
	"github.com/olitvin/skydock/utils"
	"github.com/skynetservices/skydns1/client"
	"github.com/skynetservices/skydns1/msg"
)

type Params struct {
	PathToSocket        string
	Domain              string
	Environment         string
	SkydnsURL           string
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

func initParams() {
	flag.StringVar(&params.PathToSocket, "s", "/var/run/docker.sock", "path to the docker unix socket")
	flag.StringVar(&params.SkydnsURL, "skydns", "", "url to the skydns url")
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
		log.Panicf("%s", err)
	}

	log.Println(log.INFO, "Start with params: ", string(b))
}

func validateSettings() {
	if params.Beat < 1 {
		params.Beat = params.TTL - (params.TTL / 4)
	}

	if (params.SkydnsURL != "") && (params.SkydnsContainerName != "") {
		fatal(fmt.Errorf("specify 'name' or 'skydns', not both"))
	}

	if (params.SkydnsURL == "") && (params.SkydnsContainerName == "") {
		params.SkydnsURL = "http://" + os.Getenv("SKYDNS_PORT_8080_TCP_ADDR") + ":5380"
	}

	if params.Domain == "" {
		fatal(fmt.Errorf("Must specify your skydns domain"))
	}
}

func setupLogger() error {
	log.SetSyslogHost("localhost")
	log.Initialize()

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
			log.Printf(log.ERROR, "aborting heartbeat for %s after 10 errors", uuid)
			return
		}

		// don't fill logs if we have a low params.Beat
		// may need to do something better here
		if params.Beat >= 30 {
			log.Printf(log.INFO, "updating params.TTL for %s", uuid)
		}

		if err := updateService(uuid, params.TTL); err != nil {
			errorCount++
			log.Printf(log.ERROR, "%s", err)
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
				log.Printf(log.ERROR, "failed to fetch %s on restore: %s", cnt.Id, err)
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
			log.Printf(log.ERROR, "failed to send %s to skydns on restore: %s", uuid, err)
		}
	}
	return nil
}

// sendService sends the uuid and service data to skydns
func sendService(uuid string, service *msg.Service) error {
	log.Println(log.INFO, fmt.Sprintf("adding %s (%s) to skydns", uuid, service.Name))
	if err := skydns.Add(uuid, service); err != nil {
		// ignore erros for conflicting uuids and start the heartbeat again
		if err != client.ErrConflictingUUID {
			return err
		}
		log.Printf(log.INFO, "service already exists for %s. Resetting params.TTL.", uuid)
		updateService(uuid, params.TTL)
	}
	log.Println(log.INFO, fmt.Sprintf("added %s (%s) successfully", uuid, service.Name))
	go heartbeat(uuid)
	return nil
}

func removeService(uuid string) error {
	log.Printf(log.INFO, "removing %s from skydns", uuid)
	return skydns.Delete(uuid)
}

func addService(uuid, image string) error {
	container, err := dockerClient.FetchContainer(uuid, image)
	log.Println(log.DEBUG, "container", container)
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
		log.Printf(log.DEBUG, "received event (%s)", toJson(event))
		uuid := utils.Truncate(event.ContainerId)

		switch event.Status {
		case "die", "stop", "kill":
			if err := removeService(uuid); err != nil {
				log.Printf(log.ERROR, fmt.Sprintf("error removing %s from skydns: %s", uuid, err))
			}
			log.Printf(log.ERROR, fmt.Sprintf("removed %s from skydns", uuid))
		case "start", "restart":
			if err := addService(uuid, event.Image); err != nil {
				log.Printf(log.ERROR, fmt.Sprintf("error adding %s to skydns: %s", uuid, err))
			}
		}
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "%s\n", err)
	os.Exit(1)

}

func main() {
	if err := setupLogger(); err != nil {
		fatal(err)
	}
	initParams()
	validateSettings()

	var (
		err   error
		group = &sync.WaitGroup{}
	)

	plugins, err = newRuntime(params.PluginFile)
	if err != nil {
		fatal(err)
	}

	if dockerClient, err = docker.NewClient(params.PathToSocket); err != nil {
		log.Printf(log.FATAL, "error connecting to docker: %s", err)
		fatal(err)
	}

	if params.SkydnsContainerName != "" {
		log.Printf(log.INFO, "fetch skydns container: %s", params.SkydnsContainerName)
		container, err := dockerClient.FetchContainer(params.SkydnsContainerName, "")
		if err != nil {
			log.Printf(log.FATAL, "error retrieving skydns container '%s': %s", params.SkydnsContainerName, err)
			fatal(err)
		}

		params.SkydnsURL = "http://" + container.NetworkSettings.IpAddress + ":5380"
	}

	if skydns, err = client.NewClient(params.SkydnsURL, params.Secret, params.Domain, "skydns"); err != nil {
		log.Printf(log.FATAL, "error connecting to skydns: %s", err)
		fatal(err)
	}

	/*log.Printf(log.DEBUG, "starting restore of containers")
	if err := restoreContainers(); err != nil {
		log.Printf(log.FATAL, "error restoring containers: %s", err)
		fatal(err)
	}*/

	events := dockerClient.GetEvents()

	group.Add(params.NumberOfHandlers)
	// Start event handlers
	for i := 0; i < params.NumberOfHandlers; i++ {
		go eventHandler(events, group)
	}

	log.Printf(log.DEBUG, "starting main process")
	group.Wait()
	log.Printf(log.DEBUG, "stopping cleanly via EOF")
}

func toJson(input interface{}) string {
	b, e := json.Marshal(input)
	if e != nil {
		return ""
	}
	return string(b)
}
