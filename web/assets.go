package web

import "embed"

// FS contains the browser assets for the ping monitor.
//
//go:embed static/*
var FS embed.FS
