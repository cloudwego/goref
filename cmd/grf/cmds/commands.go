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

package cmds

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/go-delve/delve/pkg/config"
	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/service/debugger"
	"github.com/spf13/cobra"

	myproc "github.com/cloudwego/goref/pkg/proc"
	"github.com/cloudwego/goref/pkg/version"
)

var (
	// rootCommand is the root of the command tree.
	rootCommand *cobra.Command

	conf        *config.Config
	loadConfErr error
	outFile     string
	maxRefDepth int

	// verbose is whether to log verbose info, like debug logs.
	verbose bool
)

// New returns an initialized command tree.
func New(docCall bool) *cobra.Command {
	// Config setup and load.
	conf, loadConfErr = config.LoadConfig()

	// Main dlv root command.
	rootCommand = &cobra.Command{
		Use:   "grf",
		Short: "Goref is a Go heap object reference analysis tool based on delve.",
		Long:  "Goref is a Go heap object reference analysis tool based on delve.",
	}
	rootCommand.CompletionOptions.DisableDefaultCmd = true

	// 'attach' subcommand.
	attachCommand := &cobra.Command{
		Use:   "attach pid [executable]",
		Short: "Attach to running process and begin scanning.",
		Long: `Attach to an already running process and begin scanning its memory.

This command will cause Goref to take control of an already running process and begin scanning object references. 
You'll have to wait for goref until it outputs 'successfully output to ...', or kill it to terminate scanning.
`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errors.New("you must provide a PID")
			}
			return nil
		},
		Run: attachCmd,
	}
	attachCommand.Flags().IntVar(&maxRefDepth, "max-depth", 0, "max reference depth shown by pprof")
	attachCommand.Flags().StringVarP(&outFile, "out", "o", "grf.out", "output file name")
	rootCommand.AddCommand(attachCommand)

	coreCommand := &cobra.Command{
		Use:   "core <executable> <core>",
		Short: "Scan a core dump.",
		Long: `Scan a core dump (only supports linux and windows core dumps).

The core command will open the specified core file and the associated executable and begin scanning object references.
You'll have to wait for goref until it outputs 'successfully output to ...', or kill it to terminate scanning.`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				return errors.New("you must provide a core file and an executable")
			}
			return nil
		},
		Run: coreCmd,
	}
	coreCommand.Flags().IntVar(&maxRefDepth, "max-depth", 0, "max reference depth shown by pprof")
	coreCommand.Flags().StringVarP(&outFile, "out", "o", "grf.out", "output file name")
	rootCommand.AddCommand(coreCommand)

	versionCommand := &cobra.Command{
		Use:   "version",
		Short: "Prints version.",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Goref Tool\n%s\n", version.Version())
			if verbose {
				fmt.Printf("Build Details: %s\n", version.BuildInfo())
			}
		},
		ValidArgsFunction: cobra.NoFileCompletions,
	}
	rootCommand.AddCommand(versionCommand)

	rootCommand.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "print verbose info or enable debug logger")

	return rootCommand
}

func attachCmd(_ *cobra.Command, args []string) {
	var pid int
	var exeFile string
	if len(args) > 0 {
		var err error
		pid, err = strconv.Atoi(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid pid: %s\n", args[0])
			os.Exit(1)
		}
	}
	if len(args) > 1 {
		exeFile = args[1]
	}
	os.Exit(execute(pid, exeFile, "", outFile, conf))
}

func coreCmd(_ *cobra.Command, args []string) {
	os.Exit(execute(0, args[0], args[1], outFile, conf))
}

func execute(attachPid int, exeFile, coreFile, outFile string, conf *config.Config) int {
	if verbose {
		if err := logflags.Setup(verbose, "", ""); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}
		defer logflags.Close()
	}
	if loadConfErr != nil {
		logflags.DebuggerLogger().Errorf("%v", loadConfErr)
	}

	if maxRefDepth > 0 {
		log.Printf("set max reference depth to %d.", maxRefDepth)
		myproc.SetMaxRefDepth(maxRefDepth)
	}

	dConf := debugger.Config{
		AttachPid:             attachPid,
		Backend:               "default",
		CoreFile:              coreFile,
		DebugInfoDirectories:  conf.DebugInfoDirectories,
		AttachWaitFor:         "",
		AttachWaitForInterval: 1,
		AttachWaitForDuration: 0,
	}
	var args []string
	if exeFile != "" {
		args = []string{exeFile}
	}
	dbg, err := debugger.New(&dConf, args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	t := dbg.Target()
	if err = myproc.ObjectReference(t, outFile); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	err = dbg.Detach(false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "detach failed: %v\n", err)
		return 1
	}

	return 0
}
