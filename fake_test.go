package main

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/xtls/xray-core/app/observatory"
	obscmd "github.com/xtls/xray-core/app/observatory/command"
	routercmd "github.com/xtls/xray-core/app/router/command"
	statscmd "github.com/xtls/xray-core/app/stats/command"
)

type fakeXray struct {
	statscmd.UnimplementedStatsServiceServer
	obscmd.UnimplementedObservatoryServiceServer
	routercmd.UnimplementedRoutingServiceServer

	mu        sync.Mutex
	sys       *statscmd.SysStatsResponse
	sysErr    error
	stats     []*statscmd.Stat
	users     []*statscmd.UserStat
	usersErr  error
	outbounds []*observatory.OutboundStatus
	obsErr    error
	balancers map[string]*routercmd.BalancerMsg
	delay     time.Duration
	calls     int
}

func (f *fakeXray) wait(ctx context.Context) error {
	f.mu.Lock()
	f.calls++
	d := f.delay
	f.mu.Unlock()
	if d == 0 {
		return nil
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *fakeXray) GetSysStats(ctx context.Context, _ *statscmd.SysStatsRequest) (*statscmd.SysStatsResponse, error) {
	if err := f.wait(ctx); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sys, f.sysErr
}

func (f *fakeXray) QueryStats(ctx context.Context, _ *statscmd.QueryStatsRequest) (*statscmd.QueryStatsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &statscmd.QueryStatsResponse{Stat: f.stats}, nil
}

func (f *fakeXray) GetUsersStats(ctx context.Context, _ *statscmd.GetUsersStatsRequest) (*statscmd.GetUsersStatsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.usersErr != nil {
		return nil, f.usersErr
	}
	return &statscmd.GetUsersStatsResponse{Users: f.users}, nil
}

func (f *fakeXray) GetOutboundStatus(ctx context.Context, _ *obscmd.GetOutboundStatusRequest) (*obscmd.GetOutboundStatusResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.obsErr != nil {
		return nil, f.obsErr
	}
	return &obscmd.GetOutboundStatusResponse{Status: &observatory.ObservationResult{Status: f.outbounds}}, nil
}

func (f *fakeXray) GetBalancerInfo(ctx context.Context, r *routercmd.GetBalancerInfoRequest) (*routercmd.GetBalancerInfoResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.balancers[r.GetTag()]
	if !ok {
		return nil, status.Error(codes.Unknown, "cannot find tag")
	}
	return &routercmd.GetBalancerInfoResponse{Balancer: b}, nil
}

type fakeServices struct {
	stats, observatory, routing bool
}

var allServices = fakeServices{true, true, true}

func startFake(t *testing.T, f *fakeXray, svc fakeServices) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := grpc.NewServer()
	if svc.stats {
		statscmd.RegisterStatsServiceServer(s, f)
	}
	if svc.observatory {
		obscmd.RegisterObservatoryServiceServer(s, f)
	}
	if svc.routing {
		routercmd.RegisterRoutingServiceServer(s, f)
	}
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.Stop)
	return lis.Addr().String()
}

func newTestExporter(t *testing.T, endpoint string, mod func(*Config)) *Exporter {
	t.Helper()
	cfg := Config{Endpoint: endpoint, Timeout: 2 * time.Second, UserLabel: UserLabelEmail}
	if mod != nil {
		mod(&cfg)
	}
	e, err := NewExporter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func populated() *fakeXray {
	return &fakeXray{
		sys: &statscmd.SysStatsResponse{
			NumGoroutine: 42, NumGC: 7, Alloc: 1000, TotalAlloc: 5000, Sys: 9000,
			Mallocs: 300, Frees: 200, LiveObjects: 100, PauseTotalNs: 1500000000, Uptime: 3600,
		},
		stats: []*statscmd.Stat{
			{Name: "inbound>>>socks-in>>>traffic>>>uplink", Value: 10},
			{Name: "inbound>>>socks-in>>>traffic>>>downlink", Value: 20},
			{Name: "outbound>>>node-a>>>traffic>>>uplink", Value: 30},
			{Name: "outbound>>>node-a>>>traffic>>>downlink", Value: 40},
			{Name: "user>>>alice@example.com>>>traffic>>>uplink", Value: 50},
			{Name: "user>>>alice@example.com>>>traffic>>>downlink", Value: 60},
			{Name: "user>>>alice@example.com>>>online", Value: 1},
			{Name: "garbage", Value: 1},
			{Name: "inbound>>>x>>>traffic>>>sideways", Value: 1},
			{Name: "system>>>x>>>traffic>>>uplink", Value: 1},
			{Name: "outbound>>>node-a>>>traffic>>>uplink", Value: 999},
		},
		users: []*statscmd.UserStat{
			{Email: "alice@example.com", Ips: []*statscmd.OnlineIPEntry{{Ip: "192.0.2.1"}, {Ip: "192.0.2.2"}}},
			{Email: "bob@example.com"},
		},
		outbounds: []*observatory.OutboundStatus{
			{OutboundTag: "node-a", Alive: true, Delay: 120, LastSeenTime: 1700000000, LastTryTime: 1700000000},
			{OutboundTag: "node-b", Alive: false, Delay: 99999999, LastErrorReason: "dial 198.51.100.7: timeout", LastSeenTime: 1690000000, LastTryTime: 1700000001},
			{OutboundTag: "node-c", Alive: true, Delay: 80, HealthPing: &observatory.HealthPingMeasurementResult{
				All: 10, Fail: 2, Average: int64(80 * time.Millisecond), Min: int64(50 * time.Millisecond),
				Max: int64(200 * time.Millisecond), Deviation: int64(10 * time.Millisecond),
			}},
			{OutboundTag: "node-d", Alive: false, HealthPing: &observatory.HealthPingMeasurementResult{All: 3, Fail: 3}},
			{OutboundTag: ""},
		},
		balancers: map[string]*routercmd.BalancerMsg{
			"pool": {
				Override:        &routercmd.OverrideInfo{},
				PrincipleTarget: &routercmd.PrincipleTargetInfo{Tag: []string{"node-a"}},
			},
			"down": {
				Override:        &routercmd.OverrideInfo{Target: "node-c"},
				PrincipleTarget: &routercmd.PrincipleTargetInfo{Tag: []string{""}},
			},
			"nostrategy": {Override: &routercmd.OverrideInfo{}},
		},
	}
}
