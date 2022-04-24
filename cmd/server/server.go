package server

type ServerManager struct {
	ServerList []IdefavServer
}

func (sm *ServerManager) Startup() error {
	for _, server := range sm.ServerList {
		err := server.Startup()
		if err != nil {
			return err
		}
	}
	return nil
}

func (sm *ServerManager) Shutdown() error {
	for _, server := range sm.ServerList {
		err := server.Shutdown()
		if err != nil {
			return err
		}
	}
	return nil
}

func (sm *ServerManager) AddServer(server IdefavServer) {
	if sm.ServerList == nil {
		sm.ServerList = []IdefavServer{}
	}
	sm.ServerList = append(sm.ServerList, server)
}

var IdefavServerManager *ServerManager = &ServerManager{}

func RegisterServer(server IdefavServer) {
	IdefavServerManager.AddServer(server)
}

type IdefavServer interface {
	Startup() error
	Shutdown() error
}
