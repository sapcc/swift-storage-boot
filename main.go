/*******************************************************************************
*
* Copyright 2016 SAP SE
*
* Licensed under the Apache License, Version 2.0 (the "License");
* you may not use this file except in compliance with the License.
* You should have received a copy of the License along with this
* program. If not, you may obtain a copy of the License at
*
*     http://www.apache.org/licenses/LICENSE-2.0
*
* Unless required by applicable law or agreed to in writing, software
* distributed under the License is distributed on an "AS IS" BASIS,
* WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
* See the License for the specific language governing permissions and
* limitations under the License.
*
*******************************************************************************/

package main

import (
	"fmt"
	"io/ioutil"
	"os"

	yaml "gopkg.in/yaml.v2"
)

//Configuration represents the content of the config file.
type Configuration struct {
	ChrootPath string   `yaml:"chroot"`
	DriveGlobs []string `yaml:"drives"`
}

//Config is the global Configuration instance that's filled by main() at
//program start.
var Config Configuration

func main() {
	//expect one argument (config file name)
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <config-file>\n", os.Args[0])
		os.Exit(1)
	}

	//read config file
	configBytes, err := ioutil.ReadFile(os.Args[1])
	if err != nil {
		Log(LogFatal, "read configuration file: %s", err.Error())
	}
	err = yaml.Unmarshal(configBytes, &Config)
	if err != nil {
		Log(LogFatal, "parse configuration: %s", err.Error())
	}

	//set working directory to the chroot directory; this simplifies file
	//system operations because we can just use relative paths to refer to
	//stuff inside the chroot
	workingDir := "/"
	if Config.ChrootPath != "" {
		workingDir = Config.ChrootPath
	}
	err = os.Chdir(workingDir)
	if err != nil {
		Log(LogFatal, "chdir to %s: %s", workingDir, err.Error())
	}

	//list drives
	drivePaths, err := ListDrives()
	if err != nil {
		Log(LogFatal, "list drives: %s", err.Error())
	}

	//look for existing mount points
	allMounts, err := ScanMountPoints()
	if err != nil {
		Log(LogFatal, "list mount points: %s", err.Error())
	}

	//try to mount all drives to /run/swift-storage (if not yet mounted)
	failed := false
	var mountPaths []string
	for _, drivePath := range drivePaths {
		mountPath, err := MountDevice("/"+drivePath, allMounts)
		if err == nil {
			mountPaths = append(mountPaths, mountPath)
		} else {
			Log(LogError, err.Error())
			failed = true
		}
	}

	//rescan mount points if we mounted something
	if len(mountPaths) > 0 {
		allMounts, err = ScanMountPoints()
		if err != nil {
			Log(LogFatal, "list mount points: %s", err.Error())
		}
	}

	//map mountpoints from /run/swift-storage to /srv/node
	mountsByID, scanFailed := ScanSwiftID(allMounts)
	if scanFailed {
		failed = true //but keep going for the drives that work
	}

	for swiftID, device := range mountsByID {
		err := ExecuteFinalMount(device, swiftID, allMounts)
		if err == nil {
			Log(LogInfo, "%s is mounted on /srv/node/%s", device, swiftID)
		} else {
			Log(LogError, err.Error())
			failed = true
		}
	}

	//mark /srv/node as ready
	_, err = ExecSimple(ExecChroot, "touch", "/srv/node/ready")
	if err != nil {
		Log(LogError, "touch /srv/node/ready: %s", err.Error())
		failed = true
	}

	//signal intermittent failures to the caller
	if failed {
		Log(LogInfo, "completed with errors, see above")
		os.Exit(1)
	}
}
