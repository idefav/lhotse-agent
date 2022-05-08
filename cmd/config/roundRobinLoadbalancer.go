package config

import (
	"sync/atomic"
)

type RoundRobinLoadBalancer struct {
	next uint32
}

func (r *RoundRobinLoadBalancer) Select(endpoints []*Endpoint) *Endpoint {
	n := atomic.AddUint32(&r.next, 1)
	return endpoints[(int(n)-1)%len(endpoints)]
}
