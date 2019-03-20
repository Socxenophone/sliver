package handlers

/*

	WARNING: These functions can be invoked by remote slivers without user interaction

*/

import (
	consts "sliver/client/constants"
	sliverpb "sliver/protobuf/sliver"
	"sliver/server/core"
	"sliver/server/log"

	"github.com/golang/protobuf/proto"
)

var (
	handlerLog = log.NamedLogger("handlers", "slivers")

	serverHandlers = map[uint32]interface{}{
		sliverpb.MsgRegister:    registerSliverHandler,
		sliverpb.MsgTunnelData:  tunnelDataHandler,
		sliverpb.MsgTunnelClose: tunnelCloseHandler,
	}
)

// GetSliverHandlers - Returns a map of server-side msg handlers
func GetSliverHandlers() map[uint32]interface{} {
	return serverHandlers
}

func registerSliverHandler(sliver *core.Sliver, data []byte) {
	register := &sliverpb.Register{}
	err := proto.Unmarshal(data, register)
	if err != nil {
		handlerLog.Warnf("error decoding message: %v", err)
		return
	}

	// If this is the first time we're getting reg info alert user(s)
	if sliver.Name == "" {
		defer func() {
			core.EventBroker.Publish(core.Event{
				EventType: consts.ConnectedEvent,
				Sliver:    sliver,
			})
		}()
	}

	sliver.Name = register.Name
	sliver.Hostname = register.Hostname
	sliver.Username = register.Username
	sliver.UID = register.Uid
	sliver.GID = register.Gid
	sliver.Os = register.Os
	sliver.Arch = register.Arch
	sliver.PID = register.Pid
	sliver.Filename = register.Filename
}

func tunnelDataHandler(sliver *core.Sliver, data []byte) {
	tunnelData := &sliverpb.TunnelData{}
	proto.Unmarshal(data, tunnelData)
	tunnel := core.Tunnels.Tunnel(tunnelData.TunnelID)
	if tunnel != nil {
		if sliver.ID == tunnel.Sliver.ID {
			tunnel.Client.Send <- &sliverpb.Envelope{
				Type: sliverpb.MsgTunnelData,
				Data: data,
			}
		} else {
			handlerLog.Warnf("Warning: Sliver %d attempted to send data on tunnel it did not own", sliver.ID)
		}
	} else {
		handlerLog.Warnf("Data sent on nil tunnel %d", tunnelData.TunnelID)
	}
}

func tunnelCloseHandler(sliver *core.Sliver, data []byte) {
	tunnelClose := &sliverpb.TunnelClose{}
	proto.Unmarshal(data, tunnelClose)
	tunnel := core.Tunnels.Tunnel(tunnelClose.TunnelID)
	if tunnel.Sliver.ID == sliver.ID {
		handlerLog.Debugf("Sliver %d closed tunnel %d (reason: %s)", sliver.ID, tunnel.ID, tunnelClose.Err)
		core.Tunnels.CloseTunnel(tunnel.ID, tunnelClose.Err)
	} else {
		handlerLog.Warnf("Warning: Sliver %d attempted to close tunnel it did not own", sliver.ID)
	}
}