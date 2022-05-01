package loadbalancer

import (
	"lhotse-agent/cmd/config"
	"sync/atomic"
)

type RoundRobinLoadBalancer struct {
	next uint32
}

func (r *RoundRobinLoadBalancer) Select(endpoints []*config.Endpoint) *config.Endpoint {
	n := atomic.AddUint32(&r.next, 1)
	return endpoints[(int(n)-1)%len(endpoints)]
}
