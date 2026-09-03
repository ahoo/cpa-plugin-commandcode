package plugin

import (
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func requestWithModel(m string) pluginapi.ModelRouteRequest {
	return pluginapi.ModelRouteRequest{RequestedModel: m}
}
