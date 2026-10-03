package main

import "api-scraper/internal/app"

var version = "dev"
var repository = ""

func main() { app.Run(version, repository) }
