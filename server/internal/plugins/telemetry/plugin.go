package telemetry

import (
	"github.com/m1k1o/neko/server/pkg/types"
)

type Plugin struct {
	config  *Config
	manager *Manager
}

func NewPlugin() *Plugin {
	return &Plugin{
		config: &Config{},
	}
}

func (p *Plugin) Name() string {
	return PluginName
}

func (p *Plugin) Config() types.PluginConfig {
	return p.config
}

func (p *Plugin) Start(m types.PluginManagers) error {
	p.manager = NewManager(m.SessionManager, p.config)
	setReporter(p.manager.reportLiveViewFailure)
	return p.manager.Start()
}

func (p *Plugin) Shutdown() error {
	setReporter(nil)
	return p.manager.Shutdown()
}
