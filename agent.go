package main

import (
	"context"
	"fmt"
	dockerTyp "github.com/docker/docker/api/types"
	dockerFilter "github.com/docker/docker/api/types/filters"
	dockerApi "github.com/docker/docker/client"
	consulApi "github.com/hashicorp/consul/api"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"strconv"
)

type Agent struct {
	consul *consulApi.Agent
	docker *dockerApi.Client
}

func (a *Agent) checkRegistrations() []error {
	log.Debug("checking service registration")
	labelListFilter := dockerFilter.NewArgs()
	labelListFilter.Add("label", "consul.service")
	containers, err := a.docker.ContainerList(context.Background(), dockerTyp.ContainerListOptions{Filters: labelListFilter})
	if err != nil {
		return []error{errors.Wrap(errors.WithStack(err), "failed to list containers")}
	}

	services, err := a.consul.Services()
	if err != nil {
		return []error{errors.Wrap(errors.WithStack(err), "failed to list consul services")}
	}

	errs := []error{}

	for _, container := range containers {
		id := container.ID
		log.WithField("container", id).Debug("checking container")
		_, consulHasService := services[id]
		if !consulHasService {
			err = a.registerContainer(&container)
			if err != nil {
				errs = append(errs, err)
			}
		}
		delete(services, id)
	}

	for _, service := range services {
		err := a.deregister(service.ID)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) != 0 {
		return errs
	}
	return nil
}

func (a *Agent) register(id string) error {
	singleListFilter := dockerFilter.NewArgs()
	singleListFilter.Add("id", id)
	singleListFilter.Add("label", "consul.service")
	containers, err := a.docker.ContainerList(context.Background(), dockerTyp.ContainerListOptions{Filters: singleListFilter})
	if err != nil {
		return NewAppErr(err, "failed to list docker container", log.Fields{"container": id})
	}
	if len(containers) == 0 {
		return nil
	}

	err = a.registerContainer(&containers[0])
	if err != nil {
		return err
	}

	return nil
}

func (a *Agent) registerContainer(container *dockerTyp.Container) error {
	log.WithField("container", container.ID).Debug("register container")
	service, err := a.containerToService(container)
	if err != nil {
		return errors.Wrap(err, "failed to covert container to service")
	}
	err = a.consul.ServiceRegister(service)
	if err != nil {
		return NewAppErr(err, "failed to register service", log.Fields{
			"id":   service.ID,
			"name": service.Name,
		})
	}
	return nil
}

func (a *Agent) deregister(id string) error {
	log.WithField("service", id).Debug("deregister service")
	err := a.consul.ServiceDeregister(id)
	if err != nil {
		return NewAppErr(err, "failed to deregister service", log.Fields{
			"id": id,
		})
	}
	return nil
}

func (a *Agent) containerToService(container *dockerTyp.Container) (*consulApi.AgentServiceRegistration, error) {
	log.WithField("container", container.ID).Debug("converting container to service")

	tags := make([]string, 0, len(container.Labels))
	for label, value := range container.Labels {
		tags = append(tags, fmt.Sprintf("%s=%s", label, value))
	}

	name := container.Labels["consul.service"]
	network, ok := container.Labels["consul.network"]
	if !ok {
		network = container.HostConfig.NetworkMode
	}
	if network == "default" {
		network = "bridge"
	}
	ip := container.NetworkSettings.Networks[network].IPAddress

	port, ok := container.Labels["consul.port"]
	if !ok {
		if len(container.Ports) == 0 {
			if len(container.Ports) == 0 {
				return nil, NewAppErr(nil, "no exposed port", log.Fields{
					"container": container.ID,
					"service":   name,
				})
			}
		}
		port = fmt.Sprint(container.Ports[0].PrivatePort)
	}

	p, err := strconv.Atoi(port)
	if err != nil {
		return nil, NewAppErr(err, fmt.Sprintf("%s in not a valid port", port), log.Fields{
			"container": container.ID,
			"service:":  name,
		})
	}

	return &consulApi.AgentServiceRegistration{
		Name:    name,
		ID:      container.ID,
		Address: ip,
		Port:    p,
		Tags:    tags,
	}, nil
}
