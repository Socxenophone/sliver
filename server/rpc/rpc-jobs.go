package rpc

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	consts "sliver/client/constants"
	clientpb "sliver/protobuf/client"
	"sliver/server/assets"
	"sliver/server/c2"
	"sliver/server/certs"
	"sliver/server/core"
	"sync"
	"time"

	"github.com/golang/protobuf/proto"
)

func rpcJobs(_ []byte, resp RPCResponse) {
	jobs := &clientpb.Jobs{
		Active: []*clientpb.Job{},
	}
	for _, job := range *core.Jobs.Active {
		jobs.Active = append(jobs.Active, &clientpb.Job{
			ID:          int32(job.ID),
			Name:        job.Name,
			Description: job.Description,
			Protocol:    job.Protocol,
			Port:        int32(job.Port),
		})
	}
	data, err := proto.Marshal(jobs)
	if err != nil {
		rpcLog.Errorf("Error encoding rpc response %v", err)
		resp([]byte{}, err)
		return
	}
	resp(data, err)
}

func rpcJobKill(data []byte, resp RPCResponse) {
	jobKillReq := &clientpb.JobKillReq{}
	err := proto.Unmarshal(data, jobKillReq)
	if err != nil {
		resp([]byte{}, err)
		return
	}
	job := core.Jobs.Job(int(jobKillReq.ID))
	jobKill := &clientpb.JobKill{ID: int32(job.ID)}
	if job != nil {
		job.JobCtrl <- true
		jobKill.Success = true
	} else {
		jobKill.Success = false
		jobKill.Err = "Invalid Job ID"
	}
	data, err = proto.Marshal(jobKill)
	resp(data, err)
}

func rpcStartMTLSListener(data []byte, resp RPCResponse) {
	mtlsReq := &clientpb.MTLSReq{}
	err := proto.Unmarshal(data, mtlsReq)
	if err != nil {
		resp([]byte{}, err)
		return
	}
	jobID, err := jobStartMTLSListener(mtlsReq.Server, uint16(mtlsReq.LPort))
	if err != nil {
		resp([]byte{}, err)
		return
	}
	data, err = proto.Marshal(&clientpb.MTLS{JobID: int32(jobID)})
	resp(data, err)
}

func jobStartMTLSListener(bindIface string, port uint16) (int, error) {

	ln, err := c2.StartMutualTLSListener(bindIface, port)
	if err != nil {
		return -1, err // If we fail to bind don't setup the Job
	}

	job := &core.Job{
		ID:          core.GetJobID(),
		Name:        "mTLS",
		Description: "mutual tls",
		Protocol:    "tcp",
		Port:        port,
		JobCtrl:     make(chan bool),
	}

	go func() {
		<-job.JobCtrl
		rpcLog.Infof("Stopping mTLS listener (%d) ...", job.ID)
		ln.Close() // Kills listener GoRoutines in startMutualTLSListener() but NOT connections

		core.Jobs.RemoveJob(job)

		core.EventBroker.Publish(core.Event{
			Job:       job,
			EventType: consts.StoppedEvent,
		})
	}()

	core.Jobs.AddJob(job)

	return job.ID, nil
}

func rpcStartDNSListener(data []byte, resp RPCResponse) {
	dnsReq := &clientpb.DNSReq{}
	err := proto.Unmarshal(data, dnsReq)
	if err != nil {
		resp([]byte{}, err)
		return
	}
	jobID, err := jobStartDNSListener(dnsReq.Domain)
	if err != nil {
		resp([]byte{}, err)
		return
	}
	data, err = proto.Marshal(&clientpb.DNS{JobID: int32(jobID)})
	resp(data, err)
}

func jobStartDNSListener(domain string) (int, error) {
	rootDir := assets.GetRootAppDir()
	certs.GetServerRSACertificatePEM(rootDir, "slivers", domain, true)
	server := c2.StartDNSListener(domain)

	job := &core.Job{
		ID:          core.GetJobID(),
		Name:        "dns",
		Description: domain,
		Protocol:    "udp",
		Port:        53,
		JobCtrl:     make(chan bool),
	}

	go func() {
		<-job.JobCtrl
		rpcLog.Infof("Stopping DNS listener (%d) ...", job.ID)
		server.Shutdown()

		core.Jobs.RemoveJob(job)

		core.EventBroker.Publish(core.Event{
			Job:       job,
			EventType: consts.StoppedEvent,
		})
	}()

	core.Jobs.AddJob(job)

	// There is no way to call DNS's ListenAndServe() without blocking
	// but we also need to check the error in the case the server
	// fails to start at all, so we setup all the Job mechanics
	// then kick off the server and if it fails we kill the job
	// ourselves.
	go func() {
		err := server.ListenAndServe()
		if err != nil {
			rpcLog.Errorf("DNS listener error %v", err)
			job.JobCtrl <- true
		}
	}()

	return job.ID, nil
}

func rpcStartHTTPSListener(data []byte, resp RPCResponse) {
	httpReq := &clientpb.HTTPReq{}
	err := proto.Unmarshal(data, httpReq)
	if err != nil {
		resp([]byte{}, err)
		return
	}

	conf := &c2.HTTPServerConfig{
		Addr:   fmt.Sprintf("%s:%d", httpReq.Iface, httpReq.LPort),
		LPort:  uint16(httpReq.LPort),
		Secure: true,
		Domain: httpReq.Domain,
		Cert:   httpReq.Cert,
		Key:    httpReq.Key,
		ACME:   httpReq.ACME,
	}
	job := jobStartHTTPListener(conf)

	data, err = proto.Marshal(&clientpb.HTTP{JobID: int32(job.ID)})
	resp(data, err)
}

func rpcStartHTTPListener(data []byte, resp RPCResponse) {
	httpReq := &clientpb.HTTPReq{}
	err := proto.Unmarshal(data, httpReq)
	if err != nil {
		resp([]byte{}, err)
		return
	}

	conf := &c2.HTTPServerConfig{
		Addr:   fmt.Sprintf("%s:%d", httpReq.Iface, httpReq.LPort),
		LPort:  uint16(httpReq.LPort),
		Domain: httpReq.Domain,
		Secure: false,
		ACME:   false,
	}
	job := jobStartHTTPListener(conf)

	data, err = proto.Marshal(&clientpb.HTTP{JobID: int32(job.ID)})
	resp(data, err)
}

func jobStartHTTPListener(conf *c2.HTTPServerConfig) *core.Job {
	server := c2.StartHTTPSListener(conf)
	name := "http"
	if conf.Secure {
		name = "https"
	}

	job := &core.Job{
		ID:          core.GetJobID(),
		Name:        name,
		Description: fmt.Sprintf("%s for domain %s", name, conf.Domain),
		Protocol:    "tcp",
		Port:        uint16(conf.LPort),
		JobCtrl:     make(chan bool),
	}
	core.Jobs.AddJob(job)

	cleanup := func(err error) {
		server.Cleanup()
		core.Jobs.RemoveJob(job)
		core.EventBroker.Publish(core.Event{
			Job:       job,
			EventType: consts.StoppedEvent,
			Err:       err,
		})
	}
	once := &sync.Once{}

	go func() {
		var err error
		if server.Conf.Secure {
			if server.Conf.ACME {
				err = server.HTTPServer.ListenAndServeTLS("", "") // ACME manager pulls the certs under the hood
			} else {
				err = listenAndServeTLS(server.HTTPServer, conf.Cert, conf.Key)
			}
		} else {
			err = server.HTTPServer.ListenAndServe()
		}
		if err != nil {
			rpcLog.Errorf("%s listener error %v", name, err)
			once.Do(func() { cleanup(err) })
			job.JobCtrl <- true // Cleanup other goroutine
		}
	}()

	go func() {
		<-job.JobCtrl
		once.Do(func() { cleanup(nil) })
	}()

	return job
}

// Fuck'in Go - https://stackoverflow.com/questions/30815244/golang-https-server-passing-certfile-and-kyefile-in-terms-of-byte-array
// basically the same as server.ListenAndServerTLS() but we can passin byte slices instead of file paths
func listenAndServeTLS(srv *http.Server, certPEMBlock, keyPEMBlock []byte) error {
	addr := srv.Addr
	if addr == "" {
		addr = ":https"
	}
	config := &tls.Config{}
	if srv.TLSConfig != nil {
		*config = *srv.TLSConfig
	}
	if config.NextProtos == nil {
		config.NextProtos = []string{"http/1.1"}
	}

	var err error
	config.Certificates = make([]tls.Certificate, 1)
	config.Certificates[0], err = tls.X509KeyPair(certPEMBlock, keyPEMBlock)
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	tlsListener := tls.NewListener(tcpKeepAliveListener{ln.(*net.TCPListener)}, config)
	return srv.Serve(tlsListener)
}

// tcpKeepAliveListener sets TCP keep-alive timeouts on accepted
// connections. It's used by ListenAndServe and ListenAndServeTLS so
// dead TCP connections (e.g. closing laptop mid-download) eventually
// go away.
type tcpKeepAliveListener struct {
	*net.TCPListener
}

func (ln tcpKeepAliveListener) Accept() (c net.Conn, err error) {
	tc, err := ln.AcceptTCP()
	if err != nil {
		return
	}
	tc.SetKeepAlive(true)
	tc.SetKeepAlivePeriod(3 * time.Minute)
	return tc, nil
}