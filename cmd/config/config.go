package config

type ProxyConfig struct {
	Services []Service `yaml:"services" json:"services"`
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
	Name     string        `yaml:"name" json:"name,omitempty"`
	Hosts    []string      `yaml:"hosts" json:"hosts,omitempty"`
	Clusters []Cluster     `yaml:"clusters" json:"clusters,omitempty"`
	Rules    RouteRuleList `yaml:"rules" json:"rules,omitempty"`
	LB       *LoadBalancer `json:"-"`
}

type Endpoint struct {
	Ip   string `yaml:"ip" json:"ip,omitempty" `
	Port string `yaml:"port" json:"port,omitempty" `
}

type SimpleLB string

var (
	ROUND_ROBIN SimpleLB
	LEAST_CONN  SimpleLB
	RANDOM      SimpleLB
	PASSTHROUGH SimpleLB
)

type ConsistentHashLB struct {
	HttpHeaderName         string `yaml:"httpHeaderName,omitempty" json:"httpHeaderName,omitempty"`
	UseSourceIp            bool   `yaml:"useSourceIp,omitempty" json:"useSourceIp,omitempty"`
	HttpQueryParameterName string `yaml:"httpQueryParameterName,omitempty" json:"httpQueryParameterName,omitempty"`
	MinimumRingSize        int    `yaml:"minimumRingSize" json:"minimumRingSize,omitempty"`
}

type LoadBalancerSettings struct {
	Simple         SimpleLB         `yaml:"simple,omitempty" json:"simple,omitempty"`
	ConsistentHash ConsistentHashLB `yaml:"consistentHash,omitempty" json:"consistentHash,omitempty"`
	LoadBalancer   *LoadBalancer    `json:"-"`
}

type TrafficPolicy struct {
	LoadBalancer LoadBalancerSettings `yaml:"loadBalancer,omitempty" json:"loadBalancer,omitempty"`
}

type Cluster struct {
	Name          string        `yaml:"name,omitempty" json:"name,omitempty"`
	Endpoints     []*Endpoint   `yaml:"endpoints,omitempty" json:"endpoints,omitempty"`
	TrafficPolicy TrafficPolicy `yaml:"trafficPolicy,omitempty" json:"trafficPolicy,omitempty"`
}

type RouteRule struct {
	ServiceName string       `yaml:"serviceName,omitempty" json:"serviceName,omitempty"`
	Http        []*HttpRoute `yaml:"http,omitempty" json:"http,omitempty"`
	Order       int32        `yaml:"order,omitempty" json:"order,omitempty"`
}

type HttpRoute struct {
	Match        []*HttpMatchRequest       `yaml:"match,omitempty" json:"match,omitempty"`
	Route        []*HttpRouteDestination   `yaml:"route,omitempty" json:"route,omitempty"`
	Redirect     *HttpRedirect             `yaml:"redirect,omitempty" json:"redirect,omitempty"`
	Rewrite      *HttpRewrite              `yaml:"rewrite,omitempty" json:"rewrite,omitempty"`
	Timeout      int32                     `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	LoadBalancer *WeightRoundRobinBalancer `json:"-"`
}

type HttpRouteDestination struct {
	Destination Destination `yaml:"destination,omitempty" json:"destination,omitempty"`
	Weight      int32       `default:"100" yaml:"weight,omitempty" json:"weight,omitempty"`
}

type HttpRedirect struct {
	Uri          string `yaml:"uri,omitempty" json:"uri,omitempty"`
	Authority    string `yaml:"authority,omitempty" json:"authority,omitempty"`
	Port         int32  `yaml:"port,omitempty" json:"port,omitempty"`
	Scheme       string `yaml:"scheme,omitempty" json:"scheme,omitempty"`
	RedirectCode string `yaml:"redirectCode,omitempty" json:"redirectCode,omitempty"`
}

type HttpRewrite struct {
	Uri       string `yaml:"uri,omitempty" json:"uri,omitempty"`
	Authority string `yaml:"authority,omitempty" json:"authority,omitempty"`
}

type HttpMatchRequest struct {
	Uri            StringMatch            `yaml:"uri,omitempty" json:"uri,omitempty"`
	Scheme         StringMatch            `yaml:"scheme,omitempty" json:"scheme,omitempty"`
	Method         StringMatch            `yaml:"method,omitempty" json:"method,omitempty"`
	Authority      StringMatch            `yaml:"authority,omitempty" json:"authority,omitempty"`
	Headers        map[string]StringMatch `yaml:"headers,omitempty" json:"headers,omitempty"`
	Port           int32                  `yaml:"port,omitempty" json:"port,omitempty"`
	SourceLabels   map[string]string      `yaml:"sourceLabels,omitempty" json:"sourceLabels,omitempty"`
	QueryParams    map[string]StringMatch `yaml:"queryParams,omitempty" json:"queryParams,omitempty"`
	IgnoreUriCase  bool                   `yaml:"ignoreUriCase,omitempty" json:"ignoreUriCase,omitempty"`
	WithoutHeaders map[string]StringMatch `yaml:"withoutHeaders,omitempty" json:"withoutHeaders,omitempty"`
}

type StringMatch struct {
	Exact  string `yaml:"exact,omitempty" json:"exact,omitempty"`
	Prefix string `yaml:"prefix,omitempty" json:"prefix,omitempty"`
	Regex  string `yaml:"regex,omitempty" json:"regex,omitempty"`
}

func (sm *StringMatch) Empty() bool {
	return !(sm.Prefix != "" || sm.Exact != "" || sm.Regex != "")
}

type Destination struct {
	Cluster string `yaml:"cluster,omitempty" json:"cluster,omitempty"`
}
