package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/criteo/ocserv-exporter/lib/occtl"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
)

type Exporter struct {
	interval    time.Duration
	listenAddr  string
	occtlCli    *occtl.Client
	promHandler http.Handler

	users []occtl.UsersMessage

	lock sync.Mutex
}

func NewExporter(occtlCli *occtl.Client, listenAddr string, interval time.Duration) *Exporter {
	return &Exporter{
		listenAddr:  listenAddr,
		interval:    interval,
		occtlCli:    occtlCli,
		promHandler: promhttp.Handler(),
	}
}

func (e *Exporter) Run() error {
	// run once to ensure we have data before starting the server
	e.update()

	go func() {
		for range time.Tick(e.interval) {
			e.update()
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", e.metricsHandler)

	log.Infof("Listening on http://%s", e.listenAddr)
	return http.ListenAndServe(e.listenAddr, mux)
}

func (e *Exporter) update() {
	e.lock.Lock()
	defer e.lock.Unlock()

	e.updateStatus()
	e.updateUsers()
}

func (e *Exporter) updateStatus() {
	status, err := e.occtlCli.ShowStatus()
	if err != nil {
		log.Errorf("Failed to get server status: %v", err)
		occtlStatusScrapeError.WithLabelValues().Inc()
		vpnActiveSessions.Reset()
		vpnHandledSessions.Reset()
		vpnIPsBanned.Reset()
		return
	}
	vpnStartTime.WithLabelValues().Set(float64(status.RawUpSince))
	vpnActiveSessions.WithLabelValues().Set(float64(status.ActiveSessions))
	vpnHandledSessions.WithLabelValues().Set(float64(status.HandledSessions))
	vpnIPsBanned.WithLabelValues().Set(float64(status.IPsBanned))
	vpnTotalAuthenticationFailures.WithLabelValues().Set(float64(status.TotalAuthenticationFailures))
	vpnSessionsHandled.WithLabelValues().Set(float64(status.SessionsHandled))
	vpnTimedOutSessions.WithLabelValues().Set(float64(status.TimedOutSessions))
	vpnTimedOutIdleSessions.WithLabelValues().Set(float64(status.TimedOutIdleSessions))
	vpnClosedDueToErrorSessions.WithLabelValues().Set(float64(status.ClosedDueToErrorSessions))
	vpnAuthenticationFailures.WithLabelValues().Set(float64(status.AuthenticationFailures))
	vpnAverageAuthTime.WithLabelValues().Set(float64(status.RawAverageAuthTime))
	vpnMaxAuthTime.WithLabelValues().Set(float64(status.RawMaxAuthTime))
	vpnAverageSessionTime.WithLabelValues().Set(float64(status.RawAverageSessionTime))
	vpnMaxSessionTime.WithLabelValues().Set(float64(status.RawMaxSessionTime))
	vpnTX.WithLabelValues().Set(float64(status.RawTX))
	vpnRX.WithLabelValues().Set(float64(status.RawRX))
}

func (e *Exporter) updateUsers() {
	e.users = nil

	vpnUserTX.Reset()
	vpnUserRX.Reset()
	vpnUserAverageTX.Reset()
	vpnUserAverageRX.Reset()
	vpnUserStartTime.Reset()
	users, err := e.occtlCli.ShowUsers()
	if err != nil {
		log.Errorf("Failed to get users details: %v", err)
		occtlUsersScrapeError.WithLabelValues().Inc()
		return
	}

	for _, user := range users {
		txSpeed, err1 := ParseSpeedString(user.AverageTX)
		rxSpeed, err2 := ParseSpeedString(user.AverageRX)
		if err1 != nil || err2 != nil {
			// 打印日志或跳过该用户
			log.Errorf("Failed to parse speed for user %s: %v, %v", user.Username, err1, err2)
			continue
		}

		vpnUserTX.WithLabelValues(user.Username, user.RemoteIP, user.MTU, user.VPNIPv4, user.VPNIPv6, user.Device, user.UserAgent).Set(float64(user.RawTX))
		vpnUserRX.WithLabelValues(user.Username, user.RemoteIP, user.MTU, user.VPNIPv4, user.VPNIPv6, user.Device, user.UserAgent).Set(float64(user.RawRX))
		vpnUserAverageTX.WithLabelValues(user.Username, user.RemoteIP, user.MTU, user.VPNIPv4, user.VPNIPv6, user.Device, user.UserAgent).Set(float64(txSpeed))
		vpnUserAverageRX.WithLabelValues(user.Username, user.RemoteIP, user.MTU, user.VPNIPv4, user.VPNIPv6, user.Device, user.UserAgent).Set(float64(rxSpeed))
		vpnUserStartTime.WithLabelValues(user.Username, user.RemoteIP, user.MTU, user.VPNIPv4, user.VPNIPv6, user.Device, user.UserAgent).Set(float64(user.RawConnectedAt))
	}
}

func (e *Exporter) metricsHandler(rw http.ResponseWriter, r *http.Request) {
	e.lock.Lock()
	defer e.lock.Unlock()
	e.promHandler.ServeHTTP(rw, r)
}

// 将 "223.9 KB/sec" 转换为 byte/sec 的 float64 数值
func ParseSpeedString(speedStr string) (float64, error) {
	parts := strings.Fields(speedStr) // ["223.9", "KB/sec"]
	if len(parts) < 2 {
		return 0, errors.New("invalid speed string")
	}

	valueStr := parts[0]              // "223.9"
	unit := strings.ToUpper(parts[1]) // "转大写"

	value, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		return 0, err
	}

	switch {
	case strings.HasPrefix(unit, "B"):
		return value, nil
	case strings.HasPrefix(unit, "KB"):
		return value * 1024, nil
	case strings.HasPrefix(unit, "MB"):
		return value * 1024 * 1024, nil
	case strings.HasPrefix(unit, "GB"):
		return value * 1024 * 1024 * 1024, nil
	default:
		return 0, errors.New("unknown unit")
	}
}
