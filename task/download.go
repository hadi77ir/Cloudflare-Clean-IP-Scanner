package task

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/Ptechgithub/CloudflareScanner/utils"
	"github.com/VividCortex/ewma"
	"github.com/hadi77ir/fragmenter"
	utls "github.com/refraction-networking/utls"
)

const (
	bufferSize                     = 1024
	defaultURL                     = "https://cf.xiu2.xyz/url"
	defaultTimeout                 = 10 * time.Second
	defaultDisableDownload         = false
	defaultTestNum                 = 10
	defaultMinSpeed        float64 = 0.0
	defaultHelloID                 = "chrome"
	defaultFragmentEnabled         = false
)

var (
	defaultFragmentOptions *fragmenter.FragmentConfig = nil
)

var (
	URL             = defaultURL
	Timeout         = defaultTimeout
	Disable         = defaultDisableDownload
	ClientHelloID   = defaultHelloID
	FragmentEnabled = defaultFragmentEnabled
	FragmentOptions = defaultFragmentOptions

	TestCount = defaultTestNum
	MinSpeed  = defaultMinSpeed
)

func checkDownloadDefault() {
	if URL == "" {
		URL = defaultURL
	}
	if Timeout <= 0 {
		Timeout = defaultTimeout
	}
	if TestCount <= 0 {
		TestCount = defaultTestNum
	}
	if MinSpeed <= 0.0 {
		MinSpeed = defaultMinSpeed
	}
}

func TestDownloadSpeed(ipSet utils.PingDelaySet) (speedSet utils.DownloadSpeedSet) {
	checkDownloadDefault()
	if Disable {
		return utils.DownloadSpeedSet(ipSet)
	}
	if len(ipSet) <= 0 {
		fmt.Println("\n[Info] The number of delay test IP addresses is 0, skipping download speed test.")
		return
	}
	testNum := TestCount
	if len(ipSet) < TestCount || MinSpeed > 0 {
		testNum = len(ipSet)
	}
	if testNum < TestCount {
		TestCount = testNum
	}

	fmt.Printf("Start download speed test (Minimum speed: %.2f MB/s, Number: %d, Queue: %d)\n", MinSpeed, TestCount, testNum)
	// Ensures that the length of the download speed progress bar matches the length of the latency progress bar (for OCD purposes)
	bar_a := len(strconv.Itoa(len(ipSet)))
	bar_b := "     "
	for i := 0; i < bar_a; i++ {
		bar_b += " "
	}
	bar := utils.NewBar(TestCount, bar_b, "")
	for i := 0; i < testNum; i++ {
		speed := downloadHandler(ipSet[i].IP)
		ipSet[i].DownloadSpeed = speed
		// After measuring the download speed for each IP, filter the results based on the [minimum download speed] condition.
		if speed >= MinSpeed*1024*1024 {
			bar.Grow(1, "")
			speedSet = append(speedSet, ipSet[i])
			if len(speedSet) == TestCount {
				break
			}
		}
	}
	bar.Done()
	if len(speedSet) == 0 {
		speedSet = utils.DownloadSpeedSet(ipSet)
	}
	// Sorts the results by speed
	sort.Sort(speedSet)
	return
}

func getDialContext(ip *net.IPAddr) func(ctx context.Context, network, address string) (net.Conn, error) {
	var fakeSourceAddr string
	if isIPv4(ip.String()) {
		fakeSourceAddr = fmt.Sprintf("%s:%d", ip.String(), TCPPort)
	} else {
		fakeSourceAddr = fmt.Sprintf("[%s]:%d", ip.String(), TCPPort)
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, fakeSourceAddr)
	}
}

// return download Speed
func downloadHandler(ip *net.IPAddr) float64 {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext:    getDialContext(ip),
			DialTLSContext: getDialTLSContext(ip),
		},
		Timeout: Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 10 {
				return http.ErrUseLastResponse
			}
			if req.Header.Get("Referer") == defaultURL {
				req.Header.Del("Referer")
			}
			return nil
		},
	}
	req, err := http.NewRequest("GET", URL, nil)
	if err != nil {
		return 0.0
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_12_6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/98.0.4758.80 Safari/537.36")

	response, err := client.Do(req)
	if err != nil {
		return 0.0
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return 0.0
	}
	timeStart := time.Now()
	timeEnd := timeStart.Add(Timeout)

	contentLength := response.ContentLength
	buffer := make([]byte, bufferSize)

	var (
		contentRead     int64 = 0
		timeSlice             = Timeout / 100
		timeCounter           = 1
		lastContentRead int64 = 0
	)

	var nextTime = timeStart.Add(timeSlice * time.Duration(timeCounter))
	e := ewma.NewMovingAverage()

	// Continuously calculates; if the file download is complete (both are equal), exits the loop (terminates speed testing)
	for contentLength != contentRead {
		currentTime := time.Now()
		if currentTime.After(nextTime) {
			timeCounter++
			nextTime = timeStart.Add(timeSlice * time.Duration(timeCounter))
			e.Add(float64(contentRead - lastContentRead))
			lastContentRead = contentRead
		}
		// If the download speed test time exceeds, exits the loop (terminates speed testing)
		if currentTime.After(timeEnd) {
			break
		}
		bufferRead, err := response.Body.Read(buffer)
		if err != nil {
			if err != io.EOF {
				break
			} else if contentLength == -1 {
				break
			}
			// Obtains the previous time slice
			last_time_slice := timeStart.Add(timeSlice * time.Duration(timeCounter-1))
			// Downloaded data amount / (current time - previous time slice / time slice)
			e.Add(float64(contentRead-lastContentRead) / (float64(currentTime.Sub(last_time_slice)) / float64(timeSlice)))
		}
		contentRead += int64(bufferRead)
	}
	return e.Value() / (Timeout.Seconds() / 120)
}

func getDialTLSContext(ip *net.IPAddr) func(ctx context.Context, network string, addr string) (net.Conn, error) {
	var fakeSourceAddr string
	if isIPv4(ip.String()) {
		fakeSourceAddr = fmt.Sprintf("%s:%d", ip.String(), TCPPort)
	} else {
		fakeSourceAddr = fmt.Sprintf("[%s]:%d", ip.String(), TCPPort)
	}
	return func(ctx context.Context, network string, addr string) (net.Conn, error) {
		dialer := &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}

		// Override the default TLS dialer
		conn, err := dialer.DialContext(ctx, "tcp", fakeSourceAddr)
		if err != nil {
			return nil, fmt.Errorf("dial error: %v", err)
		}

		// fragmenter support
		if FragmentEnabled {
			tcpConn, ok := conn.(*net.TCPConn)
			if ok {
				// Set TCP_NODELAY to true, to prevent kernel from reconstructing fragments
				_ = tcpConn.SetNoDelay(true)
			}
			conn = fragmenter.WrapConn(conn, FragmentOptions)
		}

		// Create a uTLS connection
		uConn := utls.UClient(conn, &utls.Config{
			ServerName: addr,
		}, getClientHelloId(ClientHelloID))

		// Perform the TLS handshake
		if err := uConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("TLS handshake error: %v", err)
		}
		return conn, nil
	}
}

func getClientHelloId(id string) utls.ClientHelloID {
	switch id {
	case "chrome":
		return utls.HelloChrome_Auto
	case "firefox":
		return utls.HelloFirefox_Auto
	case "safari":
		return utls.HelloSafari_Auto
	case "ios":
		return utls.HelloIOS_Auto
	case "qq":
		return utls.HelloQQ_Auto
	case "android":
		return utls.HelloAndroid_11_OkHttp
	case "edge":
		return utls.HelloEdge_Auto
	case "go":
		return utls.HelloGolang
	case "randomized":
		return utls.HelloRandomized
	case "360":
		return utls.Hello360_Auto
	}
	return utls.HelloGolang
}
