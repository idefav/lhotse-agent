package proxy

import (
	"bytes"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"lhotse-agent/cmd/mgr"
	"lhotse-agent/cmd/proxy/config"
	"lhotse-agent/cmd/proxy/constants"
	"lhotse-agent/cmd/proxy/data"
	"lhotse-agent/cmd/proxy/domainpolicy"
	"lhotse-agent/cmd/proxy/runtimeconfig"
	"lhotse-agent/cmd/server"
	"lhotse-agent/cmd/upgrade"
	"lhotse-agent/pkg/credential"
	"lhotse-agent/pkg/identity"
	"lhotse-agent/pkg/log"
	"lhotse-agent/pkg/pool"
	"lhotse-agent/pkg/tls/mitm"
)

var (
	errNoSystemCABundle = errors.New("system CA bundle not found")
	errNoActiveCA       = errors.New("active MITM CA not loaded")
)

var systemCABundlePaths = []string{
	"/etc/ssl/certs/ca-certificates.crt",
	"/etc/pki/tls/certs/ca-bundle.crt",
	"/etc/ssl/ca-bundle.pem",
	"/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
	"/etc/ssl/cert.pem",
}

type InProxyServer struct {
	Connections map[string]net.Conn
	NumOpen     int32
	IdleTimeOut time.Duration
	ConnPool    pool.Pool
	Cfg         *config.Config
	Port        int32
}

func NewInProxyServer(idleTimeOut time.Duration, port int32, cfg *config.Config) *InProxyServer {
	return &InProxyServer{
		Connections: make(map[string]net.Conn),
		NumOpen:     0,
		IdleTimeOut: idleTimeOut,
		Port:        port,
		ConnPool:    nil,
		Cfg:         cfg,
	}
}

type OutboundServer struct {
	NumOpen          int32
	IdleTimeOut      time.Duration
	Port             int32
	Cfg              *config.Config
	UpstreamRootCAs  *x509.CertPool
	CertStore        *mitm.CertStore
	CredResolver     *credential.Resolver
	IdentityResolver *identity.Resolver
	RuntimeConfig    *runtimeconfig.Manager
}

func NewOutboundServer(
	idleTimeOut time.Duration,
	port int32,
	cfg *config.Config,
	upstreamRootCAs *x509.CertPool,
	certStore *mitm.CertStore,
	credResolver *credential.Resolver,
	identityResolver *identity.Resolver,
	runtimeCfg *runtimeconfig.Manager,
) *OutboundServer {
	return &OutboundServer{
		NumOpen:          0,
		IdleTimeOut:      idleTimeOut,
		Port:             port,
		Cfg:              cfg,
		UpstreamRootCAs:  upstreamRootCAs,
		CertStore:        certStore,
		CredResolver:     credResolver,
		IdentityResolver: identityResolver,
		RuntimeConfig:    runtimeCfg,
	}
}

var ProxyCmd = &cobra.Command{
	Use:    "proxy",
	Short:  "proxy server",
	PreRun: bindFlags,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := constructCfg()

		cfg.DomainPolicy = domainpolicy.NewManager(domainpolicy.Options{
			URL:             cfg.DomainPolicyURL,
			CacheFile:       cfg.DomainPolicyCacheFile,
			RefreshInterval: cfg.DomainPolicyRefreshInterval,
			FetchTimeout:    cfg.DomainPolicyFetchTimeout,
			Scope:           cfg.DomainPolicyScope,
			AppName:         cfg.AppName,
			InstanceIP:      cfg.InstanceIP,
		})
		cfg.DomainPolicy.Start()
		data.Load(cfg)

		var certStore *mitm.CertStore
		var credResolver *credential.Resolver
		var identityResolver *identity.Resolver
		var runtimeCfg *runtimeconfig.Manager
		var upstreamRootCAs *x509.CertPool

		if cfg.MITMEnabled {
			if strings.TrimSpace(cfg.VaultURI) == "" {
				log.Fatal("mitm-enabled requires --vault-uri")
			}
			if strings.TrimSpace(cfg.AgentID) == "" {
				log.Fatal("mitm-enabled requires --agent-id")
			}
			var err error
			upstreamRootCAs, err = loadMITMUpstreamRootCAs(cfg)
			if err != nil {
				log.Fatalf("failed to initialize MITM upstream trust roots: %v", err)
			}

			runtimeCfg = runtimeconfig.NewManager(runtimeconfig.Options{
				URL:             cfg.VaultURI,
				CacheFile:       cfg.CredentialRuntimeCacheFile,
				RefreshInterval: cfg.CredentialRuntimeRefresh,
				FetchTimeout:    cfg.CredentialRuntimeFetch,
				AppName:         cfg.AppName,
				InstanceIP:      cfg.InstanceIP,
				AgentID:         cfg.AgentID,
			})
			runtimeCfg.Start()

			log.Info("MITM proxy enabled, initializing Vault components...")
			certStore = mitm.NewCertStore(cfg.VaultURI)
			credResolver = credential.NewResolver(cfg.VaultURI)
			identityResolver = identity.NewResolver(cfg.VaultURI)
			initializeCA(cfg, certStore)

			registerCABundleHandler(mgr.HttpMux, certStore)
			runtimeconfig.RegisterHandlers(mgr.HttpMux, runtimeCfg)
			startBackgroundCARefresh(cfg, certStore)
		}

		mgrServer := http.Server{
			Handler: mgr.HttpMux,
		}
		domainpolicy.RegisterHandlers(mgr.HttpMux, cfg.DomainPolicy)
		server.RegisterServer(mgr.NewManagementServer(mgrServer, ":"+strconv.Itoa(int(cfg.ProxyMgrPort))))
		server.RegisterServer(NewInProxyServer(cfg.ConnIdleTimeOut, cfg.InBoundProxyPort, cfg))
		server.RegisterServer(NewOutboundServer(
			cfg.ConnIdleTimeOut,
			cfg.OutBoundProxyPort,
			cfg,
			upstreamRootCAs,
			certStore,
			credResolver,
			identityResolver,
			runtimeCfg,
		))
		server.RegisterServer(NewUDPServer(cfg.ConnIdleTimeOut, cfg.UDPProxyPort, cfg))

		if err := server.IdefavServerManager.Startup(); err != nil {
			log.Fatal(err)
		}

		upgrade.Ready()
		upgrade.Stop(func() {
			if cfg.DomainPolicy != nil {
				cfg.DomainPolicy.Stop()
			}
			if runtimeCfg != nil {
				runtimeCfg.Stop()
			}
			_ = server.IdefavServerManager.Shutdown()
		})
	},
}

func initializeCA(cfg *config.Config, certStore *mitm.CertStore) {
	log.Infof("Waiting for CA certificate from %s...", cfg.VaultURI)
	caInitTimeout := time.After(cfg.CAInitTimeout)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := certStore.FetchAndReload(); err == nil {
				log.Info("CA certificate successfully loaded.")
				return
			} else {
				log.Errorf("Failed to fetch CA: %v. Retrying...", err)
			}
		case <-caInitTimeout:
			log.Fatalf("CA initialization timed out after %v", cfg.CAInitTimeout)
		}
	}
}

func startBackgroundCARefresh(cfg *config.Config, certStore *mitm.CertStore) {
	if cfg.CAPollInterval <= 0 {
		return
	}
	go func() {
		pollTicker := time.NewTicker(cfg.CAPollInterval)
		defer pollTicker.Stop()
		for range pollTicker.C {
			if err := certStore.FetchAndReload(); err != nil {
				log.Errorf("Background CA fetch failed: %v", err)
			}
		}
	}()
}

func registerCABundleHandler(mux *http.ServeMux, certStore *mitm.CertStore) {
	mux.HandleFunc("/ca.crt", func(w http.ResponseWriter, r *http.Request) {
		bundle, err := combinedTrustBundle(certStore)
		if err != nil {
			if errors.Is(err, errNoActiveCA) {
				http.Error(w, err.Error(), http.StatusServiceUnavailable)
				return
			}
			log.Errorf("failed to build CA bundle: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-pem-file")
		_, _ = w.Write(bundle)
	})
}

func combinedTrustBundle(certStore *mitm.CertStore) ([]byte, error) {
	systemBundle, err := loadSystemCABundle()
	if err != nil {
		return nil, err
	}
	if certStore == nil {
		return nil, errNoActiveCA
	}

	activeCA := certStore.GetActiveCAPEM()
	if len(bytes.TrimSpace(activeCA)) == 0 {
		return nil, errNoActiveCA
	}

	bundle := append([]byte(nil), systemBundle...)
	if len(bundle) > 0 && bundle[len(bundle)-1] != '\n' {
		bundle = append(bundle, '\n')
	}
	bundle = append(bundle, activeCA...)
	return bundle, nil
}

func loadSystemCABundle() ([]byte, error) {
	for _, path := range systemCABundlePaths {
		bundle, err := os.ReadFile(path)
		if err != nil || len(bytes.TrimSpace(bundle)) == 0 {
			continue
		}
		return bundle, nil
	}
	return nil, fmt.Errorf("%w in %s", errNoSystemCABundle, strings.Join(systemCABundlePaths, ", "))
}

func constructCfg() *config.Config {
	return &config.Config{
		ServerName:                  viper.GetString(constants.ServerName),
		FileName:                    viper.GetString(constants.FileName),
		CacheFileName:               viper.GetString(constants.CacheFileName),
		ProxyMgrPort:                viper.GetInt32(constants.ProxyMgrPort),
		InBoundProxyPort:            viper.GetInt32(constants.InBoundProxyPort),
		OutBoundProxyPort:           viper.GetInt32(constants.OutBoundProxyPort),
		UDPProxyPort:                viper.GetInt32(constants.UDPProxyPort),
		ConnIdleTimeOut:             viper.GetDuration(constants.ConnIdleTimeOut),
		CacheDuration:               viper.GetDuration(constants.CacheDuration),
		DomainPolicyURL:             viper.GetString(constants.DomainPolicyURL),
		DomainPolicyCacheFile:       viper.GetString(constants.DomainPolicyCacheFile),
		DomainPolicyRefreshInterval: viper.GetDuration(constants.DomainPolicyRefreshInterval),
		DomainPolicyFetchTimeout:    viper.GetDuration(constants.DomainPolicyFetchTimeout),
		DomainPolicyScope:           viper.GetString(constants.DomainPolicyScope),
		AppName:                     viper.GetString(constants.AppName),
		InstanceIP:                  viper.GetString(constants.InstanceIP),
		MITMEnabled:                 viper.GetBool(constants.MITMEnabled),
		VaultURI:                    viper.GetString(constants.VaultURI),
		AgentID:                     viper.GetString(constants.AgentID),
		UpstreamCAFile:              viper.GetString(constants.UpstreamCAFile),
		CAPollInterval:              viper.GetDuration(constants.CAPollInterval),
		CAInitTimeout:               viper.GetDuration(constants.CAInitTimeout),
		CredentialRuntimeCacheFile:  viper.GetString(constants.CredentialRuntimeCacheFile),
		CredentialRuntimeRefresh:    viper.GetDuration(constants.CredentialRuntimeRefresh),
		CredentialRuntimeFetch:      viper.GetDuration(constants.CredentialRuntimeFetch),
	}
}

func bindFlags(cmd *cobra.Command, args []string) {
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))

	for _, key := range []string{
		constants.ServerName,
		constants.FileName,
		constants.CacheFileName,
		constants.CacheDuration,
		constants.ProxyMgrPort,
		constants.InBoundProxyPort,
		constants.OutBoundProxyPort,
		constants.UDPProxyPort,
		constants.ConnIdleTimeOut,
		constants.DomainPolicyURL,
		constants.DomainPolicyCacheFile,
		constants.DomainPolicyRefreshInterval,
		constants.DomainPolicyFetchTimeout,
		constants.DomainPolicyScope,
		constants.AppName,
		constants.InstanceIP,
		constants.MITMEnabled,
		constants.VaultURI,
		constants.AgentID,
		constants.UpstreamCAFile,
		constants.CAPollInterval,
		constants.CAInitTimeout,
		constants.CredentialRuntimeCacheFile,
		constants.CredentialRuntimeRefresh,
		constants.CredentialRuntimeFetch,
	} {
		if err := viper.BindPFlag(key, cmd.Flags().Lookup(key)); err != nil {
			handleError(err)
		}
	}
}

func handleError(err error) {
	handleErrorWithCode(err, 1)
}

func handleErrorWithCode(err error, code int) {
	log.Error(err)
	os.Exit(code)
}

func bindCmdFlags(rootCmd *cobra.Command) {
	rootCmd.Flags().StringP(constants.ServerName, "s", "Lhotse Proxy", "服务器名称")
	rootCmd.Flags().StringP(constants.FileName, "c", "config.yaml", "配置文件名称")
	rootCmd.Flags().String(constants.CacheFileName, "cache.json", "缓存配置文件名称")
	rootCmd.Flags().Int32P(constants.ProxyMgrPort, "m", 15030, "Proxy服务管理端口")
	rootCmd.Flags().Int32P(constants.InBoundProxyPort, "i", 15006, "Proxy服务入口流量代理端口")
	rootCmd.Flags().Int32P(constants.OutBoundProxyPort, "o", 15001, "Proxy服务出口流量代理端口")
	rootCmd.Flags().Int32(constants.UDPProxyPort, 15009, "Proxy服务UDP流量代理端口")
	rootCmd.Flags().Duration(constants.ConnIdleTimeOut, 60*time.Second, "空闲链接默认超时时间")
	rootCmd.Flags().Duration(constants.CacheDuration, 60*time.Second, "配置定时保存时间间隔")
	rootCmd.Flags().String(constants.DomainPolicyURL, "", "Domain policy dynamic config URL")
	rootCmd.Flags().String(constants.DomainPolicyCacheFile, "/tmp/lhotse-domain-policy-cache.json", "Domain policy cache file")
	rootCmd.Flags().Duration(constants.DomainPolicyRefreshInterval, 5*time.Minute, "Domain policy refresh interval, 0 disables periodic refresh")
	rootCmd.Flags().Duration(constants.DomainPolicyFetchTimeout, 5*time.Second, "Domain policy fetch timeout")
	rootCmd.Flags().String(constants.DomainPolicyScope, domainpolicy.ScopeOutbound, "Domain policy scope: outbound, inbound, or both")
	rootCmd.Flags().String(constants.AppName, "", "Application name for dynamic domain policy fetch")
	rootCmd.Flags().String(constants.InstanceIP, "", "Instance IP for dynamic domain policy fetch")
	rootCmd.Flags().Bool(constants.MITMEnabled, false, "Enable MITM TLS proxy")
	rootCmd.Flags().String(constants.VaultURI, "", "Vault service URI for CA, identity, and credentials")
	rootCmd.Flags().String(constants.AgentID, "", "Agent ID for credential resolution")
	rootCmd.Flags().String(constants.UpstreamCAFile, "", "Additional PEM bundle for MITM upstream TLS verification")
	rootCmd.Flags().Duration(constants.CAPollInterval, 5*time.Minute, "CA certificate poll interval")
	rootCmd.Flags().Duration(constants.CAInitTimeout, 2*time.Minute, "CA certificate initialization timeout")
	rootCmd.Flags().String(constants.CredentialRuntimeCacheFile, "/tmp/lhotse-credential-runtime-cache.json", "Credential runtime cache file")
	rootCmd.Flags().Duration(constants.CredentialRuntimeRefresh, 5*time.Minute, "Credential runtime refresh interval, 0 disables periodic refresh")
	rootCmd.Flags().Duration(constants.CredentialRuntimeFetch, 5*time.Second, "Credential runtime fetch timeout")
}

func init() {
	bindCmdFlags(ProxyCmd)
}
