package main

import (
	"fmt"
	"io/ioutil"
	"os"

	"github.com/joho/godotenv"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"

	kaniko "github.com/gexops/drone-kaniko"
	"github.com/gexops/drone-kaniko/pkg/artifact"
)

const (
	// GCR JSON key file path
	gcrKeyPath     string = "/kaniko/config.json"
	gcrEnvVariable string = "GOOGLE_APPLICATION_CREDENTIALS"

	defaultDigestFile string = "/kaniko/digest-file"
)

var (
	version = "unknown"
)

func main() {
	// Load env-file if it exists first
	if env := os.Getenv("PLUGIN_ENV_FILE"); env != "" {
		if err := godotenv.Load(env); err != nil {
			logrus.Fatal(err)
		}
	}

	app := cli.NewApp()
	app.Name = "kaniko gcr plugin"
	app.Usage = "kaniko gcr plugin"
	app.Action = run
	app.Version = version
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:   "dockerfile",
			Usage:  "build dockerfile",
			Value:  "Dockerfile",
			EnvVar: "PLUGIN_DOCKERFILE",
		},
		cli.StringFlag{
			Name:   "context",
			Usage:  "build context",
			Value:  ".",
			EnvVar: "PLUGIN_CONTEXT",
		},
		cli.StringFlag{
			Name:   "drone-commit-ref",
			Usage:  "git commit ref passed by Drone",
			EnvVar: "DRONE_COMMIT_REF",
		},
		cli.StringFlag{
			Name:   "drone-repo-branch",
			Usage:  "git repository default branch passed by Drone",
			EnvVar: "DRONE_REPO_BRANCH",
		},
		cli.StringSliceFlag{
			Name:     "tags",
			Usage:    "build tags",
			Value:    &cli.StringSlice{"latest"},
			EnvVar:   "PLUGIN_TAGS",
			FilePath: ".tags",
		},
		cli.BoolFlag{
			Name:   "expand-tag",
			Usage:  "enable for semver tagging",
			EnvVar: "PLUGIN_EXPAND_TAG",
		},
		cli.BoolFlag{
			Name:   "auto-tag",
			Usage:  "enable auto generation of build tags",
			EnvVar: "PLUGIN_AUTO_TAG",
		},
		cli.StringFlag{
			Name:   "auto-tag-suffix",
			Usage:  "the suffix of auto build tags",
			EnvVar: "PLUGIN_AUTO_TAG_SUFFIX",
		},
		cli.StringSliceFlag{
			Name:   "args",
			Usage:  "build args",
			EnvVar: "PLUGIN_BUILD_ARGS",
		},
		cli.StringFlag{
			Name:   "target",
			Usage:  "build target",
			EnvVar: "PLUGIN_TARGET",
		},
		cli.StringFlag{
			Name:   "repo",
			Usage:  "gcr repository",
			EnvVar: "PLUGIN_REPO",
		},
		cli.StringSliceFlag{
			Name:   "custom-labels",
			Usage:  "additional k=v labels",
			EnvVar: "PLUGIN_CUSTOM_LABELS",
		},
		cli.StringFlag{
			Name:   "registry",
			Usage:  "gcr registry",
			Value:  "gcr.io",
			EnvVar: "PLUGIN_REGISTRY",
		},
		cli.StringSliceFlag{
			Name:   "registry-mirrors",
			Usage:  "docker registry mirrors",
			EnvVar: "PLUGIN_REGISTRY_MIRRORS",
		},
		cli.StringFlag{
			Name:   "json-key",
			Usage:  "docker username",
			EnvVar: "PLUGIN_JSON_KEY",
		},
		cli.StringFlag{
			Name:   "snapshot-mode",
			Usage:  "Specify one of full, redo or time as snapshot mode",
			EnvVar: "PLUGIN_SNAPSHOT_MODE",
		},
		cli.BoolFlag{
			Name:   "enable-cache",
			Usage:  "Set this flag to opt into caching with kaniko",
			EnvVar: "PLUGIN_ENABLE_CACHE",
		},
		cli.StringFlag{
			Name:   "cache-dir",
			Usage:  "Set this flag to specify a local directory cache for base images. enable-cache needs to be set to use this flag. Defaults to /cache.",
			Value: 	"/cache",
			EnvVar: "PLUGIN_CACHE_DIR",
		},
		cli.BoolFlag{
			Name:   "cache-copy-layers",
			Usage:  "Set this flag to cache copy layers. Defaults to false",
			EnvVar: "PLUGIN_CACHE_COPY_LAYERS",
		},
		cli.BoolFlag{
			Name:   "cache-no-compress",
			Usage:  "Set this to true in order to prevent tar compression for cached layers.",
			EnvVar: "PLUGIN_CACHE_NO_COMPRESS",
		},
		cli.StringFlag{
			Name:   "cache-repo",
			Usage:  "Remote repository that will be used to store cached layers. Cache repo should be present in specified registry. enable-cache needs to be set to use this flag",
			EnvVar: "PLUGIN_CACHE_REPO",
		},
		cli.IntFlag{
			Name:   "cache-ttl",
			Usage:  "Cache timeout in hours. Defaults to two weeks.",
			EnvVar: "PLUGIN_CACHE_TTL",
		},
		cli.StringFlag{
			Name:   "artifact-file",
			Usage:  "Artifact file location that will be generated by the plugin. This file will include information of docker images that are uploaded by the plugin.",
			EnvVar: "PLUGIN_ARTIFACT_FILE",
		},
		cli.BoolFlag{
			Name:   "no-push",
			Usage:  "Set this flag if you only want to build the image, without pushing to a registry",
			EnvVar: "PLUGIN_NO_PUSH",
		},
		cli.StringFlag{
			Name:   "verbosity",
			Usage:  "Set this flag as --verbosity=<panic|fatal|error|warn|info|debug|trace> to set the logging level for kaniko. Defaults to info.",
			EnvVar: "PLUGIN_VERBOSITY",
		},
		cli.BoolFlag{
			Name:   "use-new-run",
			Usage:  "Set this flag if experimental run implementation for detecting changes without requiring file system snapshots. In some cases, this may improve build performance by 75%",
			EnvVar: "PLUGIN_USE_NEW_RUN",
		},
		cli.StringFlag{
			Name:   "platform",
			Usage:  "Allows to build with another default platform than the host, similarly to docker build --platform",
			EnvVar: "PLUGIN_PLATFORM",
		},
	}

	if err := app.Run(os.Args); err != nil {
		logrus.Fatal(err)
	}
}

func run(c *cli.Context) error {
	noPush := c.Bool("no-push")
	jsonKey := c.String("json-key")

	// JSON key may not be set in the following cases:
	// 1. Image does not need to be pushed to GCR.
	// 2. Workload identity is set on GKE in which pod will inherit the credentials via service account.
	if jsonKey != "" {
		if err := setupGCRAuth(jsonKey); err != nil {
			return err
		}
	}

	plugin := kaniko.Plugin{
		Build: kaniko.Build{
			DroneCommitRef:  c.String("drone-commit-ref"),
			DroneRepoBranch: c.String("drone-repo-branch"),
			Dockerfile:      c.String("dockerfile"),
			Context:         c.String("context"),
			Tags:            c.StringSlice("tags"),
			AutoTag:         c.Bool("auto-tag"),
			AutoTagSuffix:   c.String("auto-tag-suffix"),
			ExpandTag:       c.Bool("expand-tag"),
			Args:            c.StringSlice("args"),
			Target:          c.String("target"),
			Repo:            fmt.Sprintf("%s/%s", c.String("registry"), c.String("repo")),
			Mirrors:         c.StringSlice("registry-mirrors"),
			Labels:          c.StringSlice("custom-labels"),
			SnapshotMode:    c.String("snapshot-mode"),
			EnableCache:     c.Bool("enable-cache"),
			CacheDir:		 c.String("cache-dir"),
			CacheCopyLayers: c.Bool("cache-copy-layers"),
			CacheNoCompress: c.Bool("cache-no-compress"),
			CacheRepo:       fmt.Sprintf("%s/%s", c.String("registry"), c.String("cache-repo")),
			CacheTTL:        c.Int("cache-ttl"),
			DigestFile:      defaultDigestFile,
			NoPush:          noPush,
			Verbosity:       c.String("verbosity"),
			UseNewRun: 		 c.Bool("use-new-run"),
			Platform:        c.String("platform"),
		},
		Artifact: kaniko.Artifact{
			Tags:         c.StringSlice("tags"),
			Repo:         c.String("repo"),
			Registry:     c.String("registry"),
			ArtifactFile: c.String("artifact-file"),
			RegistryType: artifact.GCR,
		},
	}
	return plugin.Exec()
}

func setupGCRAuth(jsonKey string) error {
	err := ioutil.WriteFile(gcrKeyPath, []byte(jsonKey), 0644)
	if err != nil {
		return errors.Wrap(err, "failed to write GCR JSON key")
	}

	err = os.Setenv(gcrEnvVariable, gcrKeyPath)
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("failed to set %s environment variable", gcrEnvVariable))
	}
	return nil
}
