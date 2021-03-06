package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	apache24Status = `localhost
ServerVersion: Apache/2.4.37 (Unix)
ServerMPM: event
Server Built: Nov  8 2018 14:43:50
CurrentTime: Thursday, 15-Nov-2018 17:01:24 CET
RestartTime: Tuesday, 13-Nov-2018 13:44:32 CET
ParentServerConfigGeneration: 1
ParentServerMPMGeneration: 0
ServerUptimeSeconds: 184611
ServerUptime: 2 days 3 hours 16 minutes 51 seconds
Load1: 0.40
Load5: 0.39
Load15: 0.43
Total Accesses: 1355598
Total kBytes: 28576363
Total Duration: 885443440
CPUUser: 258.56
CPUSystem: 620.53
CPUChildrenUser: 11485.1
CPUChildrenSystem: 19536.9
CPULoad: 17.2801
Uptime: 184611
ReqPerSec: 7.343
BytesPerSec: 158507
BytesPerReq: 21586.2
DurationPerReq: 653.176
BusyWorkers: 16
IdleWorkers: 56
Processes: 3
Stopping: 0
BusyWorkers: 16
IdleWorkers: 56
ConnsTotal: 70
ConnsAsyncWriting: 0
ConnsAsyncKeepAlive: 29
ConnsAsyncClosing: 23
Scoreboard: ........................__WRRR__K__________K____KR________________K_RRR____R___R____R___R_______..........................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................
`

	apache24WorkerStatus = `localhost
ServerVersion: Apache/2.4.23 (Unix) OpenSSL/1.0.2h
ServerMPM: worker
Server Built: Aug 31 2016 10:54:08
CurrentTime: Thursday, 08-Sep-2016 15:09:32 CEST
RestartTime: Thursday, 08-Sep-2016 15:08:07 CEST
ParentServerConfigGeneration: 1
ParentServerMPMGeneration: 0
ServerUptimeSeconds: 85
ServerUptime: 1 minute 25 seconds
Load1: 0.00
Load5: 0.01
Load15: 0.05
Total Accesses: 10
Total kBytes: 38
CPUUser: .05
CPUSystem: 0
CPUChildrenUser: 0
CPUChildrenSystem: 0
CPULoad: .0588235
Uptime: 85
ReqPerSec: .117647
BytesPerSec: 457.788
BytesPerReq: 3891.2
BusyWorkers: 2
IdleWorkers: 48
Scoreboard: _____R_______________________K____________________....................................................................................................
TLSSessionCacheStatus
CacheType: SHMCB
CacheSharedMemory: 512000
CacheCurrentEntries: 0
CacheSubcaches: 32
CacheIndexesPerSubcaches: 88
CacheIndexUsage: 0%
CacheUsage: 0%
CacheStoreCount: 0
CacheReplaceCount: 0
CacheExpireCount: 0
CacheDiscardCount: 0
CacheRetrieveHitCount: 0
CacheRetrieveMissCount: 1
CacheRemoveHitCount: 0
CacheRemoveMissCount: 0
`

	metricCountApache24       = 27
	metricCountApache24Worker = 22
)

func checkApacheStatus(t *testing.T, status string, metricCount int) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(status))
	})
	server := httptest.NewServer(handler)

	e := NewExporter(server.URL)
	ch := make(chan prometheus.Metric)

	go func() {
		defer close(ch)
		e.Collect(ch)
	}()

	for i := 1; i <= metricCount; i++ {
		m := <-ch
		if m == nil {
			t.Error("expected metric but got nil")
		}
	}
	extraMetrics := 0
	for <-ch != nil {
		extraMetrics++
	}
	if extraMetrics > 0 {
		t.Errorf("expected closed channel, got %d extra metrics", extraMetrics)
	}
}

func TestApache24Status(t *testing.T) {
	checkApacheStatus(t, apache24Status, metricCountApache24)
}

func TestApache24WorkerStatus(t *testing.T) {
	checkApacheStatus(t, apache24WorkerStatus, metricCountApache24Worker)
}
