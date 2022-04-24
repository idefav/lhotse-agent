module lhotse-agent

go 1.16

require github.com/cloudflare/tableflip v1.2.3

require github.com/spf13/cobra v1.4.0

require (
	cloud.google.com/go/logging v1.4.2
	github.com/cenkalti/backoff/v4 v4.1.2
	github.com/florianl/go-nflog/v2 v2.0.1
	github.com/go-logr/logr v1.2.3
	github.com/google/martian v2.1.0+incompatible
	github.com/miekg/dns v1.1.48
	github.com/natefinch/lumberjack v2.0.0+incompatible
	github.com/spf13/pflag v1.0.5
	github.com/spf13/viper v1.10.1
	github.com/vishvananda/netlink v1.1.1-0.20210330154013-f5de75959ad5
	go.uber.org/zap v1.21.0
	golang.org/x/net v0.0.0-20220403103023-749bd193bc2b
	golang.org/x/sys v0.0.0-20220328115105-d36c6a25d886
	google.golang.org/api v0.74.0
	google.golang.org/genproto v0.0.0-20220401170504-314d38edb7de
	google.golang.org/grpc v1.45.0
	istio.io/istio v0.0.0-20220402022427-11830ff79113
	istio.io/pkg v0.0.0-20220401180253-331f8e6246a9
	k8s.io/klog/v2 v2.60.1
)
