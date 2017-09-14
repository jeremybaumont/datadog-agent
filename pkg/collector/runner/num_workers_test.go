package runner

import (
	"runtime"
	"strconv"
	"testing"
	"time"
)

var static = false

// Efficiency test for the number of check workers running
// Not capitalized because it shouldn't be included automatically (not a unit test)
func TestUpdateNumWorkers(t *testing.T) {
	/*
		To simulate 'real' checks, it's recommended to add a delay to the TestCheck Run() function
		when running this test (note that this might cause other unit tests to fail though)
			- ie time.Sleep(time.Millisecond * 100)
		Note: use the -v flag when testing to see the output
	*/

	double := false
	for i := 0; i < 2; i++ {
		if double {
			t.Log("********* Starting the time efficiency test (double) *********")
		} else {
			t.Log("********* Starting the time efficiency test (single) *********")
		}

		t5 := timeToComplete(5, double)
		t15 := timeToComplete(15, double)
		t25 := timeToComplete(25, double)
		t35 := timeToComplete(35, double)
		t45 := timeToComplete(45, double)
		t55 := timeToComplete(55, double)
		t100 := timeToComplete(100, double)
		t200 := timeToComplete(200, double)
		t300 := timeToComplete(300, double)

		t.Logf("Time to run 5 checks: %v", t5.Seconds())
		t.Logf("Time to run 15 checks: %v", t15.Seconds())
		t.Logf("Time to run 25 checks: %v", t25.Seconds())
		t.Logf("Time to run 35 checks: %v", t35.Seconds())
		t.Logf("Time to run 45 checks: %v", t45.Seconds())
		t.Logf("Time to run 55 checks: %v", t55.Seconds())
		t.Logf("Time to run 100 checks: %v", t100.Seconds())
		t.Logf("Time to run 200 checks: %v", t200.Seconds())
		t.Logf("Time to run 300 checks: %v", t300.Seconds())

		double = true
	}

	r := NewRunner()
	curr, _ := strconv.Atoi(runnerStats.Get("Workers").String())
	runnerStats.Add("Workers", int64(curr*-1))
	m := &runtime.MemStats{}
	before := &runtime.MemStats{}

	t.Log("********* Starting memory test *********")
	runtime.ReadMemStats(m)
	runtime.ReadMemStats(before)
	t.Logf("At start:")
	t.Logf("\tAlloc = %v\tSys = %v\tHeapAlloc = %v\tHeapSys = %v\tHeapObj = %v\t",
		m.Alloc/1024, m.Sys/1024, m.HeapAlloc, m.HeapSys, m.HeapObjects)

	for i := 1; i < 500; i++ {
		c := TestCheck{name: "TestCheck" + strconv.Itoa(i)}
		if !static {
			r.UpdateNumWorkers(int64(i))
		}
		r.pending <- &c

		if i%100 == 0 {
			runtime.ReadMemStats(m)
			t.Logf("After %d checks:", i)
			t.Logf("\tAlloc = %v\tSys = %v\tHeapAlloc = %v\tHeapSys = %v\tHeapObj = %v\t",
				m.Alloc/1024, m.Sys/1024, m.HeapAlloc, m.HeapSys, m.HeapObjects)
		}
	}

	t.Logf("Difference:")
	t.Logf("\tAlloc = %v\tSys = %v\tHeapAlloc = %v\tHeapSys = %v\tHeapObj = %v\t",
		(m.Alloc-before.Alloc)/1024, (m.Sys-before.Sys)/1024,
		(m.HeapAlloc - before.HeapAlloc), (m.HeapSys - before.HeapSys),
		(m.HeapObjects - before.HeapObjects))
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
