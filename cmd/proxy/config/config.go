package config

import (
	"time"

	"lhotse-agent/cmd/proxy/domainpolicy"
)

type Config struct {
	ServerName                  string
	FileName                    string
	ProxyMgrPort                int32
	InBoundProxyPort            int32
	OutBoundProxyPort           int32
	UDPProxyPort                int32
	ConnIdleTimeOut             time.Duration
	CacheFileName               string
	CacheDuration               time.Duration
	DomainPolicy                *domainpolicy.Manager
	DomainPolicyURL             string
	DomainPolicyCacheFile       string
	DomainPolicyRefreshInterval time.Duration
	DomainPolicyFetchTimeout    time.Duration
	DomainPolicyScope           string
	AppName                     string
	InstanceIP                  string
	MITMEnabled                 bool
	VaultURI                    string
	AgentID                     string
	UpstreamCAFile              string
	CAPollInterval              time.Duration
	CAInitTimeout               time.Duration
	CredentialRuntimeCacheFile  string
	CredentialRuntimeRefresh    time.Duration
	CredentialRuntimeFetch      time.Duration
}
