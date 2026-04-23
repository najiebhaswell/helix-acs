package cwmp

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/raykavin/helix-acs/internal/datamodel"
)

func TestExtractWANInfos_TR098_MultiWANShowsPPPoEAndTR069(t *testing.T) {
	mapper := &datamodel.TR098Mapper{
		WANDeviceIdx:  1,
		WANConnDevIdx: 1,
		WANIPConnIdx:  1,
		WANPPPConnIdx: 1,
	}

	params := map[string]string{
		// TR069 management WAN (IP_Routed)
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.ConnectionType":    "IP_Routed",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.ConnectionStatus":  "Connected",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.ExternalIPAddress": "10.201.7.60",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.X_CT-COM_ServiceList": "TR069",

		// Internet WAN PPPoE on a different WANConnectionDevice
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.2.WANPPPConnection.1.ConnectionStatus":  "Connected",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.2.WANPPPConnection.1.ExternalIPAddress": "100.64.3.38",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.2.WANPPPConnection.1.Username":          "gmediax665",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.2.WANPPPConnection.1.X_CT-COM_ServiceList": "Internet",
	}

	wans := extractWANInfos(params, mapper, nil)
	assert.Len(t, wans, 2)

	// Internet PPPoE should be prioritized before management TR069.
	assert.Equal(t, "PPPoE", wans[0].ConnectionType)
	assert.Equal(t, "Connected", wans[0].LinkStatus)
	assert.Equal(t, "100.64.3.38", wans[0].IPAddress)
	assert.Equal(t, "gmediax665", wans[0].PPPoEUsername)
	assert.Equal(t, "Internet", wans[0].ServiceType)

	assert.Equal(t, "IP_Routed", wans[1].ConnectionType)
	assert.Equal(t, "10.201.7.60", wans[1].IPAddress)
	assert.Equal(t, "TR069", wans[1].ServiceType)
}
