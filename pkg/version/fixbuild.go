// Copyright 2024 CloudWeGo Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build go1.18

package version

import "runtime/debug"

func init() {
	fixBuild = buildInfoFixBuild
}

func buildInfoFixBuild(v *Version) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	for i := range info.Settings {
		if info.Settings[i].Key == "vcs.revision" {
			v.Build = info.Settings[i].Value
			break
		}
	}
}
