// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2017 Datadog, Inc.

package config

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	log "github.com/cihub/seelog"
)

const logFileMaxSize = 10 * 1024 * 1024         // 10MB
const logDateFormat = "2006-01-02 15:04:05 MST" // see time.Format for format syntax

var logCertPool *x509.CertPool

// SetupLogger sets up the default logger
func SetupLogger(logLevel, logFile, uri string, rfc, tls bool, pem string) error {
	var syslog bool

	if uri != "" { // non-blank uri enables syslog
		syslog = true
	}

	if pem != "" {
		if logCertPool == nil {
			logCertPool = x509.NewCertPool()
		}
		logCertPool.AppendCertsFromPEM([]byte(pem))
	}

	configTemplate := `<seelog minlevel="%s">
    <outputs formatid="common">
        <console />`
	if logFile != "" {
		configTemplate += `<rollingfile type="size" filename="%s" maxsize="%d" maxrolls="1" />`
	}
	if syslog {
		var syslogTemplate string
		if uri != "" {
			syslogTemplate = fmt.Sprintf(
				`<custom name="syslog" formatid="syslog" data-uri="%s" data-tls="%v" />`,
				uri,
				tls,
			)
		} else {
			syslogTemplate = `<custom name="syslog" formatid="syslog" />`
		}
		configTemplate += syslogTemplate
	}
	configTemplate += `</outputs>
    <formats>
        <format id="common" format="%%Date(%s) | %%LEVEL | (%%File:%%Line in %%FuncShort) | %%Msg%%n"/>`
	if syslog {
		if rfc {
			configTemplate += `<format id="syslog" format="%%CustomSyslogHeader(20,true) %%LEVEL | (%%RelFile:%%Line) | %%Msg%%n" />`
		} else {
			configTemplate += `<format id="syslog" format="%%CustomSyslogHeader(20,false) %%LEVEL | (%%RelFile:%%Line) | %%Msg%%n" />`
		}
	}

	configTemplate += `</formats>
</seelog>`
	config := fmt.Sprintf(configTemplate, strings.ToLower(logLevel), logFile, logFileMaxSize, logDateFormat)

	logger, err := log.LoggerFromConfigAsString(config)
	if err != nil {
		return err
	}
	log.ReplaceLogger(logger)
	return nil
}

// ErrorLogWriter is a Writer that logs all written messages with the global seelog logger
// at an error level
type ErrorLogWriter struct{}

func (s *ErrorLogWriter) Write(p []byte) (n int, err error) {
	log.Error(string(p))
	return len(p), nil
}

var levelToSyslogSeverity = map[log.LogLevel]int{
	// Mapping to RFC 5424 where possible
	log.TraceLvl:    7,
	log.DebugLvl:    7,
	log.InfoLvl:     6,
	log.WarnLvl:     4,
	log.ErrorLvl:    3,
	log.CriticalLvl: 2,
	log.Off:         7,
}

func createSyslogHeaderFormatter(params string) log.FormatterFunc {
	facility := 20
	rfc := false

	ps := strings.Split(params, ",")
	if len(ps) == 2 {
		i, err := strconv.Atoi(ps[0])
		if err == nil && i >= 0 && i <= 23 {
			facility = i
		}

		rfc = (ps[1] == "true")
	} else {
		fmt.Printf("badly formatted syslog header parameters - using defaults")
	}

	pid := os.Getpid()
	appName := filepath.Base(os.Args[0])

	if rfc { // RFC 5424
		return func(message string, level log.LogLevel, context log.LogContextInterface) interface{} {
			return fmt.Sprintf("<%d>1 %s %d - -", facility*8+levelToSyslogSeverity[level], appName, pid)
		}
	}

	// otherwise old-school logging
	return func(message string, level log.LogLevel, context log.LogContextInterface) interface{} {
		return fmt.Sprintf("<%d>%s[%d]:", facility*8+levelToSyslogSeverity[level], appName, pid)
	}
}

// SyslogReceiver implements seelog.CustomReceiver
type SyslogReceiver struct {
	enabled bool
	uri     *url.URL
	tls     bool
	conn    net.Conn
}

func getSyslogConnection(uri *url.URL, secure bool) (net.Conn, error) {
	var conn net.Conn
	var err error

	// local
	localNetNames := []string{"unixgram", "unix"}
	if uri == nil {
		addrs := []string{"/dev/log", "/var/run/syslog", "/var/run/log"}
		for _, netName := range localNetNames {
			for _, addr := range addrs {
				conn, err = net.Dial(netName, addr)
				if err == nil { // on success
					return conn, nil
				}
			}
		}
	} else {
		switch uri.Scheme {
		case "unix", "unixgram":
			fmt.Printf("Trying to connecto to: %s", uri.Path)
			for _, netName := range localNetNames {
				conn, err = net.Dial(netName, uri.Path)
				if err == nil {
					break
				}
			}
		case "udp":
			conn, err = net.Dial(uri.Scheme, uri.Host)
		case "tcp":
			if secure {
				conn, err = tls.Dial("tcp", uri.Host, &tls.Config{RootCAs: logCertPool})
			} else {
				conn, err = net.Dial("tcp", uri.Host)
			}
		}
		if err == nil {
			return conn, nil
		}
	}

	return nil, errors.New("Unable to connect to syslog")
}

// ReceiveMessage process current log message
func (s *SyslogReceiver) ReceiveMessage(message string, level log.LogLevel, context log.LogContextInterface) error {
	if !s.enabled {
		return nil
	}

	if s.conn != nil {
		_, err := s.conn.Write([]byte(message))
		if err == nil {
			return nil
		}
	}

	// try to reconnect - close the connection first just in case
	//                    we don't want fd leaks here.
	if s.conn != nil {
		s.conn.Close()
	}
	conn, err := getSyslogConnection(s.uri, s.tls)
	if err != nil {
		return err
	}

	s.conn = conn
	_, err = s.conn.Write([]byte(message))
	fmt.Printf("Retried: %v\n", message)
	return err
}

// AfterParse parses the receiver configuration
func (s *SyslogReceiver) AfterParse(initArgs log.CustomReceiverInitArgs) error {
	var conn net.Conn
	var ok bool
	var err error

	s.enabled = true
	uri, ok := initArgs.XmlCustomAttrs["uri"]
	if ok && uri != "" {
		url, err := url.ParseRequestURI(uri)
		if err != nil {
			s.enabled = false
		}

		s.uri = url
	}

	tls, ok := initArgs.XmlCustomAttrs["tls"]
	if ok {
		// if certificate specified it should already be in pool
		if tls == "true" {
			s.tls = true
		}
	}

	if !s.enabled {
		return errors.New("bad syslog receiver configuration - disabling")
	}

	conn, err = getSyslogConnection(s.uri, s.tls)
	if err != nil {
		fmt.Printf("%v\n", err)
		return nil
	}
	s.conn = conn

	return nil
}

// Flush is a NOP in current implementation
func (s *SyslogReceiver) Flush() {
	// Nothing to do here...
}

// Close is a NOP in current implementation
func (s *SyslogReceiver) Close() error {
	return nil
}

func init() {
	log.RegisterCustomFormatter("CustomSyslogHeader", createSyslogHeaderFormatter)
	log.RegisterReceiver("syslog", &SyslogReceiver{})
}
