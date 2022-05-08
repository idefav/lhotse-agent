package config

import "math/rand"

type LastConnLoadBalancer struct {
	Last int
}

func (l *LastConnLoadBalancer) Select(endpoints []*Endpoint) *Endpoint {
	size := len(endpoints)
	if l.Last < 0 || l.Last > size-1 {
		if len(endpoints) <= 0 {
			return nil
		}
		size := len(endpoints)
		index := rand.Intn(size)
		l.Last = index
		return endpoints[index]
	} else {
		return endpoints[l.Last]
	}

}
