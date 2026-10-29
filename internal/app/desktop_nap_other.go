//go:build !(darwin && cgo)

package app

func holdOffAppNap(bool) {}
