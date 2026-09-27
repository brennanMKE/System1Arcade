package main

import "testing"

// TestBeginActivity checks that an App Nap activity can be held and released
// repeatedly, and overlapping, as agent restarts do.
func TestBeginActivity(t *testing.T) {
	a := beginActivity("test")
	b := beginActivity("test 2")
	a()
	b()
	beginActivity("test 3")()
}
