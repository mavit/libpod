package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"github.com/projectatomic/buildah/imagebuildah"
	buildahcli "github.com/projectatomic/buildah/pkg/cli"
	"github.com/projectatomic/buildah/pkg/parse"
	"github.com/projectatomic/libpod/cmd/podman/libpodruntime"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

var (
	buildDescription = "podman build launches the Buildah command to build an OCI Image. Buildah must be installed for this command to work."
	buildCommand     = cli.Command{
		Name:           "build",
		Usage:          "Build an image using instructions in a Dockerfile",
		Description:    buildDescription,
		Flags:          append(buildahcli.BudFlags, buildahcli.FromAndBudFlags...),
		Action:         buildCmd,
		ArgsUsage:      "CONTEXT-DIRECTORY | URL",
		SkipArgReorder: true,
	}
)

func buildCmd(c *cli.Context) error {
	// The following was taken directly from projectatomic/buildah/cmd/bud.go
	// TODO Find a away to vendor more of this in rather than copy from bud
	output := ""
	tags := []string{}
	if c.IsSet("tag") || c.IsSet("t") {
		tags = c.StringSlice("tag")
		if len(tags) > 0 {
			output = tags[0]
			tags = tags[1:]
		}
	}
	pullPolicy := imagebuildah.PullNever
	if c.BoolT("pull") {
		pullPolicy = imagebuildah.PullIfMissing
	}
	if c.Bool("pull-always") {
		pullPolicy = imagebuildah.PullAlways
	}

	args := make(map[string]string)
	if c.IsSet("build-arg") {
		for _, arg := range c.StringSlice("build-arg") {
			av := strings.SplitN(arg, "=", 2)
			if len(av) > 1 {
				args[av[0]] = av[1]
			} else {
				delete(args, av[0])
			}
		}
	}

	dockerfiles := c.StringSlice("file")
	format := "oci"
	if c.IsSet("format") {
		format = strings.ToLower(c.String("format"))
	}
	if strings.HasPrefix(format, "oci") {
		format = imagebuildah.OCIv1ImageFormat
	} else if strings.HasPrefix(format, "docker") {
		format = imagebuildah.Dockerv2ImageFormat
	} else {
		return errors.Errorf("unrecognized image type %q", format)
	}
	contextDir := ""
	cliArgs := c.Args()
	if len(cliArgs) > 0 {
		// The context directory could be a URL.  Try to handle that.
		tempDir, subDir, err := imagebuildah.TempDirForURL("", "buildah", cliArgs[0])
		if err != nil {
			return errors.Wrapf(err, "error prepping temporary context directory")
		}
		if tempDir != "" {
			// We had to download it to a temporary directory.
			// Delete it later.
			defer func() {
				if err = os.RemoveAll(tempDir); err != nil {
					logrus.Errorf("error removing temporary directory %q: %v", contextDir, err)
				}
			}()
			contextDir = filepath.Join(tempDir, subDir)
		} else {
			// Nope, it was local.  Use it as is.
			absDir, err := filepath.Abs(cliArgs[0])
			if err != nil {
				return errors.Wrapf(err, "error determining path to directory %q", cliArgs[0])
			}
			contextDir = absDir
		}
		cliArgs = cliArgs.Tail()
	} else {
		// No context directory or URL was specified.  Try to use the
		// home of the first locally-available Dockerfile.
		for i := range dockerfiles {
			if strings.HasPrefix(dockerfiles[i], "http://") ||
				strings.HasPrefix(dockerfiles[i], "https://") ||
				strings.HasPrefix(dockerfiles[i], "git://") ||
				strings.HasPrefix(dockerfiles[i], "github.com/") {
				continue
			}
			absFile, err := filepath.Abs(dockerfiles[i])
			if err != nil {
				return errors.Wrapf(err, "error determining path to file %q", dockerfiles[i])
			}
			contextDir = filepath.Dir(absFile)
			dockerfiles[i], err = filepath.Rel(contextDir, absFile)
			if err != nil {
				return errors.Wrapf(err, "error determining path to file %q", dockerfiles[i])
			}
			break
		}
	}
	if contextDir == "" {
		return errors.Errorf("no context directory specified, and no dockerfile specified")
	}
	if len(dockerfiles) == 0 {
		dockerfiles = append(dockerfiles, filepath.Join(contextDir, "Dockerfile"))
	}
	if err := parse.ValidateFlags(c, buildahcli.BudFlags); err != nil {
		return err
	}

	runtimeFlags := []string{}
	for _, arg := range c.StringSlice("runtime-flag") {
		runtimeFlags = append(runtimeFlags, "--"+arg)
	}
	// end from buildah

	runtime, err := libpodruntime.GetRuntime(c)
	if err != nil {
		return errors.Wrapf(err, "could not get runtime")
	}
	defer runtime.Shutdown(false)

	systemContext, err := parse.SystemContextFromOptions(c)
	if err != nil {
		return errors.Wrapf(err, "error building system context")
	}

	commonOpts, err := parse.ParseCommonBuildOptions(c)
	if err != nil {
		return err
	}

	options := imagebuildah.BuildOptions{
		ContextDirectory:      contextDir,
		PullPolicy:            pullPolicy,
		Compression:           imagebuildah.Gzip,
		Quiet:                 c.Bool("quiet"),
		SignaturePolicyPath:   c.String("signature-policy"),
		Args:                  args,
		Output:                output,
		AdditionalTags:        tags,
		Runtime:               c.String("runtime"),
		RuntimeArgs:           runtimeFlags,
		OutputFormat:          format,
		SystemContext:         systemContext,
		CommonBuildOpts:       commonOpts,
		DefaultMountsFilePath: c.GlobalString("default-mounts-file"),
	}

	if !c.Bool("quiet") {
		options.ReportWriter = os.Stderr
	}

	return runtime.Build(getContext(), options, dockerfiles...)
}
