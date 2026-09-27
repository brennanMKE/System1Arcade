//go:build !darwin

package main

// beginActivity is a no-op outside macOS, which has no App Nap.
func beginActivity(reason string) (end func()) { return func() {} }
