package data

import (
	"errors"
	"fmt"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"lhotse-agent/cmd/config"
	"lhotse-agent/cmd/config/loadbalancer"
	"lhotse-agent/pkg/log"
)

type MapsMatch interface {
	GetService(host string) *config.Service
	GetRule(service string) *config.RouteRule
	GetServiceRule(host string) *config.RouteRule
	GetEndpoints(serviceId string) *config.Endpoint
	GetCluster(serviceId string) *config.Cluster
}

type Maps struct {
	ServiceMap map[string]*config.Service
	RuleMap    map[string]*config.RouteRule
	Endpoints  map[string]map[string]*config.Endpoint
	Clusters   map[string]map[string]*config.Cluster
}

func NewMaps() *Maps {
	return &Maps{
		ServiceMap: make(map[string]*config.Service),
		RuleMap:    make(map[string]*config.RouteRule),
		Endpoints:  map[string]map[string]*config.Endpoint{},
		Clusters:   map[string]map[string]*config.Cluster{},
	}
}

func (m *Maps) GetService(host string) *config.Service {
	return m.ServiceMap[host]
}

func (m *Maps) GetRule(service string) *config.RouteRule {
	return m.RuleMap[service]
}

func (m *Maps) GetServiceRule(host string) *config.RouteRule {
	service := m.GetService(host)
	if service == nil {
		return nil
	}
	return m.GetRule(service.Name)
}

func (m *Maps) GetEndpoints(serviceId string) []*config.Endpoint {
	endpointMap := m.Endpoints[serviceId]
	var endpoints []*config.Endpoint
	for _, endpoint := range endpointMap {
		endpoints = append(endpoints, endpoint)
	}
	return endpoints
}

func (m *Maps) GetCluster(serviceId string) []*config.Cluster {
	clustersMap := m.Clusters[serviceId]
	var clusters []*config.Cluster
	for _, cluster := range clustersMap {
		clusters = append(clusters, cluster)
	}
	return clusters
}

func (m *Maps) LoadServiceData(file string) {
	buf, err := ioutil.ReadFile(file)
	if err != nil {
		log.Error(err)
		return
	}
	var pc = &config.ProxyConfig{}
	err = yaml.Unmarshal(buf, pc)
	if err != nil {
		log.Error(err)
		return
	}
	for _, service := range pc.Services {
		if service.LB == nil {
			var balancer config.LoadBalancer = &loadbalancer.RoundRobinLoadBalancer{}
			service.LB = &balancer
		}
		clusterMap, ok := m.Clusters[service.Name]
		if !ok {
			clusterMap = make(map[string]*config.Cluster, 0)
			m.Clusters[service.Name] = clusterMap
		}
		endpointMap, ok := m.Endpoints[service.Name]
		if !ok {
			endpointMap = make(map[string]*config.Endpoint, 0)
			m.Endpoints[service.Name] = endpointMap
		}
		for _, host := range service.Hosts {
			m.ServiceMap[host] = &service
			for _, cluster := range service.Clusters {
				clusterMap[cluster.Name] = &cluster
				m.Clusters[service.Name] = clusterMap
				for _, endpoint := range cluster.Endpoints {
					endpointMap[fmt.Sprintf("%s:%s", endpoint.Ip, endpoint.Port)] = endpoint
					m.Endpoints[service.Name] = endpointMap
				}
			}
		}
	}

	for _, rule := range pc.Rules {
		m.RuleMap[rule.ServiceName] = &rule
	}
}

func removeDuplicateElement(endpoints []*config.Endpoint) []*config.Endpoint {
	result := make([]*config.Endpoint, 0, len(endpoints))
	temp := map[string]struct{}{}
	for _, item := range endpoints {
		var key = fmt.Sprintf("%s:%s", item.Ip, item.Port)
		if _, ok := temp[key]; !ok {
			temp[key] = struct{}{}
			result = append(result, item)
		}
	}
	return result
}

var ServiceData = NewMaps()

func Match(host string) (*config.Endpoint, error) {
	service := ServiceData.GetService(host)
	if service == nil {
		return nil, errors.New("no service")
	}
	rule := ServiceData.GetRule(service.Name)
	if rule == nil {
		endpoints := ServiceData.GetEndpoints(service.Name)
		if len(endpoints) <= 0 {
			return nil, errors.New("no cluster")
		}
		//
		balancer := *service.LB
		endpoint := balancer.Select(endpoints)
		return endpoint, nil
	}
	return nil, errors.New("no endpoint")
}
