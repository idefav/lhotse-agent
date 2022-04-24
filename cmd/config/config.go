package config

type Service struct {
	Name     string   `yaml:"name" json:"name,omitempty"`
	Hosts    []string `yaml:"hosts" json:"hosts,omitempty"`
	Clusters []Cluster
}

type Endpoint struct {
	Ip   string `yaml:"ip" json:"ip,omitempty"`
	Port string `yaml:"port" json:"port,omitempty"`
}

type SimpleLB string

var (
	ROUND_ROBIN SimpleLB
	LEAST_CONN  SimpleLB
	RANDOM      SimpleLB
	PASSTHROUGH SimpleLB
)

type ConsistentHashLB struct {
	httpHeaderName         string
	useSourceIp            bool
	httpQueryParameterName string
	minimumRingSize        int
}

type LoadBalancerSettings struct {
	simple         SimpleLB
	consistentHash ConsistentHashLB
}

type TrafficPolicy struct {
	loadBalancer LoadBalancerSettings
}

type Cluster struct {
	Name          string
	Endpoints     []Endpoint
	TrafficPolicy TrafficPolicy
}

type RouteRule struct {
	Name        string
	ServiceName string
	HttpRule    HttpRoute
}

type HttpRoute struct {
	Name     string
	Match    HttpMatchRequest
	Route    []HttpRouteDestination
	Redirect HttpRedirect
	Rewrite  HttpRewrite
	Timeout  int32
}

type HttpRouteDestination struct {
	Destination Destination
	Weight      int32
}

type HttpRedirect struct {
	Uri          string
	Authority    string
	Port         int32
	Scheme       string
	RedirectCode string
}

type HttpRewrite struct {
	Uri       string
	Authority string
}

type HttpMatchRequest struct {
	Name           string
	Uri            StringMatch
	Scheme         StringMatch
	Method         StringMatch
	Authority      StringMatch
	Headers        map[string]StringMatch
	Port           int32
	SourceLabels   map[string]string
	QueryParams    map[string]StringMatch
	IgnoreUriCase  bool
	WithoutHeaders map[string]StringMatch
}

type StringMatch struct {
	Exact  string
	Prefix string
	Regex  string
}

type Destination struct {
	Subset string
}
