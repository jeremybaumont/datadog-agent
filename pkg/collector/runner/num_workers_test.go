package runner

import (
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/datadog-agent/pkg/collector/check"
	"github.com/DataDog/datadog-agent/pkg/collector/py"
	log "github.com/cihub/seelog"
	"github.com/sbinet/go-python"
)

/****************** Testing configuration ****************************/

var testingEfficiency = true // Run this test (should be false normally)
var static = false           // Run with default num workers (vs dynamic)
var prettyOutput = false     // Labelled test results
var checkType = pythonCheck  // Which type of check to run (busyWait, lazyWait, or pythonCheck)
var numIntervals = 10        // How many times to repeat each check (for the time tests)

/*********************************************************************/

type CheckType int

const (
	busyWait    CheckType = iota
	lazyWait    CheckType = iota
	pythonCheck CheckType = iota
)

// NumWorkersCheck implements the 'Check' interface via the TestCheck struct defined in runner_test.go
type NumWorkersCheck struct {
	TestCheck
	name string
}

type stickyLock struct {
	gstate python.PyGILState
	locked uint32 // Flag set to 1 if the lock is locked, 0 otherwise
}

func (sl *stickyLock) unlock() {
	atomic.StoreUint32(&sl.locked, 0)
	python.PyGILState_Release(sl.gstate)
	runtime.UnlockOSThread()
}

func (nc *NumWorkersCheck) String() string { return nc.name }
func (nc *NumWorkersCheck) ID() check.ID   { return check.ID(nc.String()) }

func (nc *NumWorkersCheck) Run() error {
	switch checkType {
	case busyWait:
		start := time.Now()
		now := time.Now()
		for {
			if now.Sub(start) > time.Millisecond*100 {
				break
			}
			now = time.Now()
		}

	case lazyWait:
		time.Sleep(time.Millisecond * 100)

	case pythonCheck:

		log.Debugf("Attempting to run a python check.")

		// Lock the GIL while operating with go-python
		/*
			runtime.LockOSThread()
			gstate := &stickyLock{
				gstate: python.PyGILState_Ensure(),
				locked: 1,
			}
			defer gstate.unlock()

			/*
				// Define a check instance with the check.py module
				config.Datadog.Set("foo_agent", "bar_agent")
				defer config.Datadog.Set("foo_agent", nil)
				module := python.PyImport_ImportModule("check")
				if module == nil {
					python.PyErr_Print()
					panic("Unable to import check")
				}

				// Import the TestCheck class
				checkClass := module.GetAttrString("TestCheck")
				if checkClass == nil {
					python.PyErr_Print()
					panic("Unable to load TestCheck class")
				}

				// Run the check
				check := py.NewPythonCheck("Python_Test_Check", checkClass)
				e := check.Run()
				if e != nil {
					nc.hasRun = false
					return e
				}
		*/

		tuple := python.PyTuple_New(0)
		res := py.NewPythonCheck("check", tuple)

		if res != nil {
			log.Debug("pass")
		} else {
			log.Debug("fail")
		}

	}

	nc.hasRun = true
	return nil
}

// Efficiency test for the number of check workers running
// Note: use the -v flag when testing to see the output
func TestUpdateNumWorkers(t *testing.T) {
	if !testingEfficiency {
		return
	}

	log.Debugf("********* Starting a single check *********")
	start := time.Now()
	r := NewRunner()
	c := NumWorkersCheck{name: "Check"}
	if !static {
		r.UpdateNumWorkers(1)
	}
	r.pending <- &c
	close(r.pending)
	Wg.Wait()

	log.Debugf("Time to run the check: %v", time.Now().Sub(start).Seconds())

	return
	/*
		// Run the time tests
		interval := false
		for i := 0; i < 2; i++ {
			if interval {
				t.Logf("********* Starting the time efficiency test (%v repeats) *********", numIntervals)
			} else {
				t.Log("********* Starting the time efficiency test (single) *********")
			}

			checksToRun := [10]int{5, 15, 25, 35, 45, 55, 65, 75, 85, 100}

			for _, n := range checksToRun {
				ti := timeToComplete(n, interval)

				if prettyOutput {
					t.Logf("Time to run %v checks: %v", n, ti.Seconds())
				} else {
					t.Logf("%v", ti.Seconds())
				}
			}

			interval = true
		}

		// Run the memory test
		r := NewRunner()
		curr, _ := strconv.Atoi(runnerStats.Get("Workers").String())
		runnerStats.Add("Workers", int64(curr*-1))
		m := &runtime.MemStats{}

		t.Log("********* Starting memory test *********")
		runtime.ReadMemStats(m)

		if prettyOutput {
			t.Logf("At start:")
			t.Logf("\tAlloc = %v\tSys = %v\tHeapAlloc = %v\tHeapSys = %v\tHeapObj = %v\t",
				m.Alloc/1024, m.Sys/1024, m.HeapAlloc, m.HeapSys, m.HeapObjects)
		} else {
			t.Logf("%v\t%v\t%v\t%v\t%v\t", m.Alloc/1024, m.Sys/1024, m.HeapAlloc, m.HeapSys, m.HeapObjects)
		}

		for i := 1; i < 500; i++ {
			c := NumWorkersCheck{name: "Check" + strconv.Itoa(i)}
			if !static {
				r.UpdateNumWorkers(int64(i))
			}
			r.pending <- &c

			if i%100 == 0 {
				runtime.ReadMemStats(m)

				if prettyOutput {
					t.Logf("After %d checks:", i)
					t.Logf("\tAlloc = %v\tSys = %v\tHeapAlloc = %v\tHeapSys = %v\tHeapObj = %v\t",
						m.Alloc/1024, m.Sys/1024, m.HeapAlloc, m.HeapSys, m.HeapObjects)
				} else {
					t.Logf("%v\t%v\t%v\t%v\t%v\t", m.Alloc/1024, m.Sys/1024, m.HeapAlloc, m.HeapSys, m.HeapObjects)
				}
			}
		}
	*/
}

func timeToComplete(numChecks int, runMultiple bool) time.Duration {
	r := NewRunner()

	// Reset the stats to having 4 workers
	curr, _ := strconv.Atoi(runnerStats.Get("Workers").String())
	runnerStats.Add("Workers", int64(curr*-1)+4)

	start := time.Now()

	// Initialize the correct number of checks in the channel
	for i := 1; i < numChecks; i++ {
		c := NumWorkersCheck{name: "Check" + strconv.Itoa(i)}
		if !static {
			r.UpdateNumWorkers(int64(i))
		}
		r.pending <- &c
	}

	if runMultiple {
		// To imitate a check running at an interval (UpdateNumWorkers doesn't run again)
		for j := 0; j < numIntervals; j++ {
			for i := 1; i < numChecks; i++ {
				c := NumWorkersCheck{name: "Check" + strconv.Itoa(i)}
				r.pending <- &c
			}
		}
	}
	close(r.pending)

	// Wait for all the checks to finish
	Wg.Wait()

	return time.Now().Sub(start)
}
