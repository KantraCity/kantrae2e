//go:build !android

package main

import "time"

const defaultServer = "https://localhost"

const deviceKind = "desktop"

// keepAlive: heartbeat after this much silence (laptops sleep, NATs drop
// idle connections; a desktop does not care about radio wake-ups).
const keepAlive = 90 * time.Second

func dbPath() string { return defaultDBPath() }
