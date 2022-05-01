package loadbalancer

import (
	"lhotse-agent/cmd/config"
	"math/rand"
)

type RandomLoadBalancer struct {
}

func (r *RandomLoadBalancer) Select(endpoints []*config.Endpoint) *config.Endpoint {
	if len(endpoints) <= 0 {
		return nil
	}
	size := len(endpoints)
	index := rand.Intn(size)
	return endpoints[index]
}
