package runner

import (
	"runtime"
	"strconv"
	"testing"
	"time"
)

// Testing configuration
var testingEfficiency = false // Run this test
var busyWait = true           // Run with busywait (vs lazy wait)
var static = false            // Run with default num workers (vs dynamic)
var prettyOutput = true       // Labelled test results

// Efficiency test for the number of check workers running
// Note: use the -v flag when testing to see the output
func TestUpdateNumWorkers(t *testing.T) {
	if !testingEfficiency {
		return
	}

	// Run the time tests
	double := false
	for i := 0; i < 2; i++ {
		if double {
			t.Log("********* Starting the time efficiency test (double) *********")
		} else {
			t.Log("********* Starting the time efficiency test (single) *********")
		}

		checksToRun := [11]int{5, 15, 25, 35, 45, 55, 65, 100, 200, 300, 400}

		for _, n := range checksToRun {
			ti := timeToComplete(n, double)

			if prettyOutput {
				t.Logf("Time to run %v checks: %v", n, ti.Seconds())
			} else {
				t.Logf("%v", ti.Seconds())
			}
		}

		double = true
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
		c := TestCheck{name: "TestCheck" + strconv.Itoa(i)}
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
}

func timeToComplete(numChecks int, runTwice bool) time.Duration {
	r := NewRunner()

	// Reset the stats to having 4 workers
	curr, _ := strconv.Atoi(runnerStats.Get("Workers").String())
	runnerStats.Add("Workers", int64(curr*-1)+4)

	start := time.Now()

	// Initialize the correct number of checks in the channel
	for i := 1; i < numChecks; i++ {
		c := TestCheck{name: "TestCheck" + strconv.Itoa(i)}
		if !static {
			r.UpdateNumWorkers(int64(i))
		}
		r.pending <- &c
	}

	if runTwice {
		// To imitate a check running at an interval
		for i := 1; i < numChecks; i++ {
			c := TestCheck{name: "TestCheck" + strconv.Itoa(i)}
			r.pending <- &c
		}
	}
	close(r.pending)

	// Wait for all the checks to finish
	Wg.Wait()

	return time.Now().Sub(start)
}
