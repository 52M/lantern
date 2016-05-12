package analytics

import (
	"bytes"
	"math"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/getlantern/eventual"
	"github.com/getlantern/flashlight/client"
	"github.com/getlantern/flashlight/config"
	"github.com/getlantern/flashlight/geolookup"
	"github.com/getlantern/flashlight/util"

	"github.com/getlantern/errors"
	"github.com/getlantern/golog"
)

const (
	trackingId  = "UA-21815217-12"
	ApiEndpoint = `https://ssl.google-analytics.com/collect`
)

var (
	log = golog.LoggerFor("flashlight.analytics")

	maxWaitForIP = math.MaxInt32 * time.Second
)

func Start(cfg *config.Config, version string) func() {
	var addr atomic.Value
	go func() {
		ip := geolookup.GetIP(maxWaitForIP)
		if ip == "" {
			errors.New("No IP found").WithOp("geolookup").
				With("waitSeconds", strconv.FormatInt(int64(maxWaitForIP/time.Second), 10)).Report()
			return
		}
		addr.Store(ip)
		log.Debugf("Starting analytics session with ip %v", ip)
		startSession(ip, version, client.Addr, cfg.Client.DeviceID)
	}()

	stop := func() {
		if addr.Load() != nil {
			ip := addr.Load().(string)
			log.Debugf("Ending analytics session with ip %v", ip)
			endSession(ip, version, client.Addr, cfg.Client.DeviceID)
		}
	}
	return stop
}

func sessionVals(ip, version, clientId, sc string) string {
	vals := make(url.Values, 0)

	vals.Add("v", "1")
	vals.Add("cid", clientId)
	vals.Add("tid", trackingId)

	// Override the users IP so we get accurate geo data.
	vals.Add("uip", ip)

	// Make call to anonymize the user's IP address -- basically a policy thing where
	// Google agrees not to store it.
	vals.Add("aip", "1")

	vals.Add("dp", "localhost")
	vals.Add("t", "pageview")

	// Custom variable for the Lantern version
	vals.Add("cd1", version)

	// This forces the recording of the session duration. It must be either
	// "start" or "end". See:
	// https://developers.google.com/analytics/devguides/collection/protocol/v1/parameters
	vals.Add("sc", sc)
	return vals.Encode()
}

func endSession(ip string, version string, proxyAddrFN eventual.Getter, clientId string) {
	args := sessionVals(ip, version, clientId, "end")
	trackSession(args, proxyAddrFN)
}

func startSession(ip string, version string, proxyAddrFN eventual.Getter, clientId string) {
	args := sessionVals(ip, version, clientId, "start")
	trackSession(args, proxyAddrFN)
}

func trackSession(args string, proxyAddrFN eventual.Getter) {
	r, err := http.NewRequest("POST", ApiEndpoint, bytes.NewBufferString(args))
	if err != nil {
		errors.Report(err)
		return
	}

	r.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Add("Content-Length", strconv.Itoa(len(args)))

	if req, err := httputil.DumpRequestOut(r, true); err != nil {
		errors.Wrap(err).WithOp("dump-request").Report()
	} else {
		log.Debugf("Full analytics request: %v", string(req))
	}

	var httpClient *http.Client
	httpClient, err = util.HTTPClient("", proxyAddrFN)
	if err != nil {
		errors.Report(err)
		return
	}
	resp, err := httpClient.Do(r)
	if err != nil {
		errors.Report(err)
		return
	}
	log.Debugf("Successfully sent request to GA: %s", resp.Status)
	if err := resp.Body.Close(); err != nil {
		log.Debugf("Unable to close response body: %v", err)
	}
}
