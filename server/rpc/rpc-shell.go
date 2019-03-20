package rpc

import (
	sliverpb "sliver/protobuf/sliver"
	"sliver/server/core"

	"github.com/golang/protobuf/proto"
)

func rpcShell(req []byte, resp RPCResponse) {
	shellReq := &sliverpb.ShellReq{}
	proto.Unmarshal(req, shellReq)

	sliver := core.Hive.Sliver(shellReq.SliverID)
	tunnel := core.Tunnels.Tunnel(shellReq.TunnelID)

	startShellReq, err := proto.Marshal(&sliverpb.ShellReq{
		EnablePTY: shellReq.EnablePTY,
		TunnelID:  tunnel.ID,
	})
	if err != nil {
		resp([]byte{}, err)
		return
	}
	rpcLog.Infof("Requesting Sliver %d to start shell", sliver.ID)
	data, err := sliver.Request(sliverpb.MsgShellReq, defaultTimeout, startShellReq)
	rpcLog.Infof("Sliver %d responded to shell start request", sliver.ID)
	resp(data, err)
}