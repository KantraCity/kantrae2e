package main

// buildServer is the gateway address preset in a build (empty: the
// platform default). CI writes it from the KANTRA_SERVER repository
// variable into server_preset_ci.go, so apps come with your domain filled in.
var buildServer string
