package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/knthmn/rclone-watchsync/fs/watchsync"
	_ "github.com/rclone/rclone/backend/all" // import all backends
	"github.com/rclone/rclone/cmd"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/flags"
)

var (
	delay = 2
)

func init() {
	cmd.Root.AddCommand(commandDefinition)
	cmdFlags := commandDefinition.Flags()
	flags.IntVarP(cmdFlags, &delay, "delay", "", delay, "Set the delay (in seconds) before the change is processed")
}

var commandDefinition = &cobra.Command{
	Use:   "watchsync source:path dest:path",
	Short: "Make source and dest identical, modifying destination only",
	// TODO long description
	Run: func(command *cobra.Command, args []string) {
		cmd.CheckArgs(2, 2, command, args)
		fsrc, srcFileName, fdst := cmd.NewFsSrcFileDst(args)
		if srcFileName != "" {
			fs.Errorf(fsrc, "Must be watching a directory")
			return
		}
		watchsync.WatchSync(context.Background(), fdst, fsrc, delay)
	},
}

func main() {
	cmd.Main()
}
