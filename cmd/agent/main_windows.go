// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2017 Datadog, Inc.

package main

import (
	_ "expvar"
	"fmt"
	_ "net/http/pprof"
	"os"
	"strings"
	"time"

	"github.com/DataDog/datadog-agent/cmd/agent/app"
	"github.com/DataDog/datadog-agent/cmd/agent/common"
	"github.com/DataDog/datadog-agent/cmd/agent/common/signals"
	log "github.com/cihub/seelog"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/debug"
	"golang.org/x/sys/windows/svc/eventlog"
)

var elog debug.Log

func setupLogger(logLevel string) error {
	configTemplate := `<seelog minlevel="%s">
    <outputs formatid="common"><console/></outputs>
    <formats>
        <format id="common" format="%%LEVEL | (%%RelFile:%%Line) | %%Msg%%n"/>
    </formats>
</seelog>`
	config := fmt.Sprintf(configTemplate, strings.ToLower(logLevel))

	logger, err := log.LoggerFromConfigAsString(config)
	if err != nil {
		return err
	}
	err = log.ReplaceLogger(logger)
	if err != nil {
		return err
	}
	return nil
}

func main() {
	isIntSess, err := svc.IsAnInteractiveSession()
	if err != nil {
		fmt.Printf("failed to determine if we are running in an interactive session: %v", err)
	}
	if !isIntSess {
		common.EnableLoggingToFile()
		runService(false)
		return
	}
	defer log.Flush()

	// Invoke the Agent
	if err := app.AgentCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(-1)
	}
}

type myservice struct{}

func (m *myservice) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}
	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

	if err := common.ImportRegistryConfig(); err != nil {
		elog.Warning(2, fmt.Sprintf("Failed to import config items from registry %s", err.Error()))
		// continue running agent with existing config
	}
	if err := common.CheckAndUpgradeConfig(); err != nil {
		log.Warn("failed to upgrade config %s", err.Error())
		// continue running with what we have.
	}
	app.StartAgent()

loop:
	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
				// Testing deadlock from https://code.google.com/p/winsvc/issues/detail?id=4
				time.Sleep(100 * time.Millisecond)
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				app.StopAgent()
				break loop
			default:
				elog.Error(1, fmt.Sprintf("unexpected control request #%d", c))
			}
		case <-signals.Stopper:
			elog.Info(1, "Received stop command, shutting down...")
			app.StopAgent()
			break loop

		}
	}
	elog.Info(1, fmt.Sprintf("prestopping %s service", app.ServiceName))
	changes <- svc.Status{State: svc.StopPending}
	return
}

func runService(isDebug bool) {
	var err error
	if isDebug {
		elog = debug.New(app.ServiceName)
	} else {
		elog, err = eventlog.Open(app.ServiceName)
		if err != nil {
			return
		}
	}
	defer elog.Close()

	elog.Info(1, fmt.Sprintf("starting %s service", app.ServiceName))
	run := svc.Run
	if isDebug {
		run = debug.Run
	}
	err = run(app.ServiceName, &myservice{})
	if err != nil {
		elog.Error(1, fmt.Sprintf("%s service failed: %v", app.ServiceName, err))
		return
	}
	elog.Info(1, fmt.Sprintf("%s service stopped", app.ServiceName))
}
