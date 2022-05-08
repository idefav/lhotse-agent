package config

type PassThroughLoadBalancer struct {
}

func (p *PassThroughLoadBalancer) Select(endpoints []*Endpoint) *Endpoint {
	return nil
}
