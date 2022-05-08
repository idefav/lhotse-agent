package config

import (
	"math/rand"
)

type RandomLoadBalancer struct {
}

func (r *RandomLoadBalancer) Select(endpoints []*Endpoint) *Endpoint {
	if len(endpoints) <= 0 {
		return nil
	}
	size := len(endpoints)
	index := rand.Intn(size)
	return endpoints[index]
}
