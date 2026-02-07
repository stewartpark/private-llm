package main

import _ "embed"

//go:embed config/vm-startup.sh
var vmStartupScript string

//go:embed config/Caddyfile
var caddyfileContent string
