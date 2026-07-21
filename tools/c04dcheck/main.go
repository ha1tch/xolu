// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package main

import "golang.org/x/tools/go/analysis/singlechecker"

func main() { singlechecker.Main(Analyzer) }
