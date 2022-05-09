package config

import "time"

type Config struct {
	ServerName        string
	FileName          string
	ProxyMgrPort      int32
	InBoundProxyPort  int32
	OutBoundProxyPort int32
	ConnIdleTimeOut   time.Duration
	CacheFileName     string
	CacheDuration     time.Duration
}
