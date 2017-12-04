package main

import (
	"context"
	dockerTyp "github.com/docker/docker/api/types"
	dockerFilter "github.com/docker/docker/api/types/filters"
	dockerApi "github.com/docker/docker/client"
	consulApi "github.com/hashicorp/consul/api"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"os"
	"time"
)

func parseCli() {

	flags := pflag.NewFlagSet("junction", pflag.ExitOnError)
	flags.StringP("consul-agent-endpoint", "e", "http://localhost:8500", "local consul agent endpoint")
	flags.StringP("docker-socket", "", "unix:///var/run/docker.sock", "docker endpoint location")
	flags.StringP("log-level", "l", "error", "debug, info, warn, erro")
	flags.BoolP("run-once", "", false, "exit after initial service registration")

	viper.BindPFlags(flags)
	viper.SetEnvPrefix("DCB")
	viper.AutomaticEnv()

	err := flags.Parse(os.Args[1:])
	if err != nil {
		log.WithError(err).Fatal("failed to parse cli arguments")
	}
}

func main() {

	parseCli()
	level, err := log.ParseLevel(viper.GetString("log-level"))
	if err != nil {
		log.WithError(err).Fatal("failed to parse cli options")
	}
	log.SetLevel(level)

	if log.GetLevel() == log.DebugLevel {
		viper.Debug()
	}

	docker, err := dockerApi.NewEnvClient()
	if err != nil {
		log.WithError(err).Fatal("failed to connect to docker")
	}

	consul, err := consulApi.NewClient(
		&consulApi.Config{
			Address: viper.GetString("endpoints"),
		})
	if err != nil {
		log.WithError(err).WithField("address", "consul-agent.kontena.local:8500").Fatal("failed to connect to consul")
	}
	agent := &Agent{
		consul: consul.Agent(),
		docker: docker,
	}

	if viper.GetBool("run-once") {
		once(agent)
	} else {
		loop(agent)
	}
}

func loop(agent *Agent) {
	connectFilter := dockerFilter.NewArgs()
	connectFilter.Add("type", "network")
	connectFilter.Add("event", "connect")

	disconnectFilter := dockerFilter.NewArgs()
	disconnectFilter.Add("type", "network")
	disconnectFilter.Add("event", "disconnect")

	connectEventer, connectErrC := agent.docker.Events(context.Background(), dockerTyp.EventsOptions{Filters: connectFilter})
	disconnectEventer, disconnectErrC := agent.docker.Events(context.Background(), dockerTyp.EventsOptions{Filters: disconnectFilter})

	ticker := time.NewTicker(30 * time.Second)

	errs := agent.checkRegistrations()
	if errs != nil {
		for _, err := range errs {
			logError(err)
		}
	}

	for {
		select {
		case msg := <-connectEventer:
			log.WithField("msg", msg).Debug("connetct event")
			err := agent.register(msg.Actor.Attributes["container"])
			if err != nil {
				logError(err)
			}
		case msg := <-disconnectEventer:
			err := agent.deregister(msg.Actor.Attributes["container"])
			log.WithField("msg", msg).Debug("disconnetct event")
			if err != nil {
				logError(err)
			}

		case <-ticker.C:
			errs := agent.checkRegistrations()
			if errs != nil {
				for _, err := range errs {
					logError(err)
				}
			}
		case err := <-connectErrC:
			logError(errors.Wrap(err, "failed to handel network connect event"))

		case err := <-disconnectErrC:
			logError(errors.Wrap(err, "failed to handel network disconnect event"))
		}
	}
}

func logError(err error) {
	cause := errors.Cause(err)
	appErr, ok := cause.(*AppErr)
	if !ok {
		log.Error(err)
		return
	}
	log.WithFields(appErr.fields).Error(appErr)
}

func once(agent *Agent) {
	errs := agent.checkRegistrations()
	if errs != nil {
		for _, err := range errs {
			logError(err)
		}
	}
}
