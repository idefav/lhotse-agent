package config

type LoadBalancer interface {
	Select(endpoints []*Endpoint) *Endpoint
}
