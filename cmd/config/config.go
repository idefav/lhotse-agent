package config

type ProxyConfig struct {
	Services []Service `yaml:"services"`
}

type RouteRuleList []RouteRule

func (r RouteRuleList) Len() int {
	return len(r)
}

func (r RouteRuleList) Less(i, j int) bool {
	return r[i].Order < r[j].Order
}

func (r RouteRuleList) Swap(i, j int) {
	r[i], r[j] = r[j], r[i]
}

type Service struct {
	Name     string        `yaml:"name"`
	Hosts    []string      `yaml:"hosts"`
	Clusters []Cluster     `yaml:"clusters"`
	Rules    RouteRuleList `yaml:"rules"`
	LB       *LoadBalancer
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
	HttpHeaderName         string `yaml:"httpHeaderName"`
	UseSourceIp            bool   `yaml:"useSourceIp"`
	HttpQueryParameterName string `yaml:"httpQueryParameterName"`
	MinimumRingSize        int    `yaml:"minimumRingSize"`
}

type LoadBalancerSettings struct {
	Simple         SimpleLB         `yaml:"simple"`
	ConsistentHash ConsistentHashLB `yaml:"consistentHash"`
	LoadBalancer   *LoadBalancer
}

type TrafficPolicy struct {
	LoadBalancer LoadBalancerSettings `yaml:"loadBalancer"`
}

type Cluster struct {
	Name          string        `yaml:"name"`
	Endpoints     []*Endpoint   `yaml:"endpoints"`
	TrafficPolicy TrafficPolicy `yaml:"trafficPolicy"`
}

type RouteRule struct {
	ServiceName string       `yaml:"serviceName"`
	Http        []*HttpRoute `yaml:"http"`
	Order       int32        `yaml:"order"`
}

type HttpRoute struct {
	Match        []*HttpMatchRequest     `yaml:"match"`
	Route        []*HttpRouteDestination `yaml:"route"`
	Redirect     *HttpRedirect           `yaml:"redirect"`
	Rewrite      *HttpRewrite            `yaml:"rewrite"`
	Timeout      int32                   `yaml:"timeout"`
	LoadBalancer *WeightRoundRobinBalancer
}

type HttpRouteDestination struct {
	Destination Destination `yaml:"destination"`
	Weight      int32       `default:"100"" yaml:"weight"`
}

type HttpRedirect struct {
	Uri          string `yaml:"uri"`
	Authority    string `yaml:"authority"`
	Port         int32  `yaml:"port"`
	Scheme       string `yaml:"scheme"`
	RedirectCode string `yaml:"redirectCode"`
}

type HttpRewrite struct {
	Uri       string `yaml:"uri"`
	Authority string `yaml:"authority"`
}

type HttpMatchRequest struct {
	Uri            StringMatch            `yaml:"uri"`
	Scheme         StringMatch            `yaml:"scheme"`
	Method         StringMatch            `yaml:"method"`
	Authority      StringMatch            `yaml:"authority"`
	Headers        map[string]StringMatch `yaml:"headers"`
	Port           int32                  `yaml:"port"`
	SourceLabels   map[string]string      `yaml:"sourceLabels"`
	QueryParams    map[string]StringMatch `yaml:"queryParams"`
	IgnoreUriCase  bool                   `yaml:"ignoreUriCase"`
	WithoutHeaders map[string]StringMatch `yaml:"withoutHeaders"`
}

type StringMatch struct {
	Exact  string `yaml:"exact"`
	Prefix string `yaml:"prefix"`
	Regex  string `yaml:"regex"`
}

func (sm *StringMatch) Empty() bool {
	return !(sm.Prefix != "" || sm.Exact != "" || sm.Regex != "")
}

type Destination struct {
	Cluster string `yaml:"cluster"`
}
