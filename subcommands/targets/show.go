package targets

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/cheynewallace/tabby"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/foundriesio/fioctl/client"
)

func init() {
	cmd.AddCommand(&cobra.Command{
		Use:   "show <version> [<app>]",
		Short: "Show details of a specific target.",
		Run:   doShow,
		Args:  cobra.RangeArgs(1, 2),
	})
}

func showApp(factory, targetName, appName string, custom *client.TufCustom) {
	_, ok := custom.ComposeApps[appName]
	if !ok {
		fmt.Println("ERROR: App not found in target")
		os.Exit(1)
	}
	bundle, err := api.TargetApp(factory, targetName, appName)
	if err != nil {
		fmt.Print("ERROR:", err)
		os.Exit(1)
	}
	fmt.Println("Files:")
	for _, file := range bundle.Files {
		fmt.Println("\t" + file)
	}

	fmt.Println("\ndocker-compose data:")
	pretty, err := json.MarshalIndent(bundle.ComposeRef, "", "  ")
	if err != nil {
		fmt.Print("ERROR:", err)
		os.Exit(1)
	}
	fmt.Println(string(pretty))
}

func doShow(cmd *cobra.Command, args []string) {
	factory := viper.GetString("factory")
	logrus.Debugf("Showing target for %s %s", factory, args[0])
	var app *string
	if len(args) == 2 {
		app = &args[1]
	}

	targets, err := api.TargetsList(factory)
	if err != nil {
		fmt.Print("ERROR: ")
		fmt.Println(err)
		os.Exit(1)
	}

	hashes := make(map[string]string)
	var tags []string
	var apps map[string]client.DockerApp
	var composeApps map[string]client.ComposeApp
	containersSha := ""
	manifestSha := ""
	overridesSha := ""
	for name, target := range targets.Signed.Targets {
		custom, err := api.TargetCustom(target)
		if err != nil {
			fmt.Printf("ERROR: %s\n", err)
			continue
		}
		if custom.Version != args[0] {
			continue
		}
		if custom.TargetFormat != "OSTREE" {
			logrus.Debugf("Skipping non-ostree target: %v", target)
			continue
		}
		if app != nil {
			showApp(factory, name, *app, custom)
			return
		}
		if len(custom.ContainersSha) > 0 {
			if len(containersSha) > 0 && containersSha != custom.ContainersSha {
				fmt.Println("ERROR: Git hashes for containers.git does not match across platforms")
				os.Exit(1)
			}
			containersSha = custom.ContainersSha
		}
		if len(custom.LmpManifestSha) > 0 {
			if len(manifestSha) > 0 && manifestSha != custom.LmpManifestSha {
				fmt.Println("ERROR: Git hashes for lmp-manifest.git does not match across platforms")
				os.Exit(1)
			}
			manifestSha = custom.LmpManifestSha
		}
		if len(custom.OverridesSha) > 0 {
			if len(overridesSha) > 0 && overridesSha != custom.OverridesSha {
				fmt.Println("ERROR: Git hashes for meta-subscriber-overrides.git does not match across platforms")
				os.Exit(1)
			}
			overridesSha = custom.OverridesSha
		}
		hashes[name] = base64.StdEncoding.EncodeToString(target.Hashes["sha256"])
		apps = custom.DockerApps
		composeApps = custom.ComposeApps
		tags = custom.Tags
	}

	fmt.Printf("Tags:\t%s\n", strings.Join(tags, ","))

	fmt.Printf("CI:\thttps://ci.foundries.io/projects/%s/lmp/builds/%s/\n", factory, args[0])

	fmt.Println("Source:")
	if len(manifestSha) > 0 {
		fmt.Printf("\thttps://source.foundries.io/factories/%s/lmp-manifest.git/commit/?id=%s\n", factory, manifestSha)
	}
	if len(overridesSha) > 0 {
		fmt.Printf("\thttps://source.foundries.io/factories/%s/meta-subscriber-overrides.git/commit/?id=%s\n", factory, overridesSha)
	}
	if len(containersSha) > 0 {
		fmt.Printf("\thttps://source.foundries.io/factories/%s/containers.git/commit/?id=%s\n", factory, containersSha)
	}
	fmt.Println("")

	t := tabby.New()
	t.AddHeader("TARGET NAME", "OSTREE HASH - SHA256")
	for name, val := range hashes {
		t.AddLine(name, val)
	}
	t.Print()

	fmt.Println()

	if len(apps) > 0 {
		t = tabby.New()
		t.AddHeader("DOCKER APP", "VERSION")
		for name, app := range apps {
			if len(app.FileName) > 0 {
				t.AddLine(name, app.FileName)
			}
			if len(app.Uri) > 0 {
				t.AddLine(name, app.Uri)
			}
		}
		t.Print()
	}
	if len(composeApps) > 0 {
		if len(apps) > 0 {
			fmt.Println()
		}
		t = tabby.New()
		t.AddHeader("COMPOSE APP", "VERSION")
		for name, app := range composeApps {
			t.AddLine(name, app.Uri)
		}
		t.Print()
	}
}
