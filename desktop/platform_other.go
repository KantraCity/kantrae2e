//go:build !android

package main

const defaultServer = "https://localhost"

const deviceKind = "desktop"

func dbPath() string { return defaultDBPath() }
