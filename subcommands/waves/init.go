package waves

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strconv"
	"time"

	canonical "github.com/docker/go/canonical/json"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/foundriesio/fioctl/client"
	"github.com/foundriesio/fioctl/subcommands"
	"github.com/foundriesio/fioctl/subcommands/keys"
	tuf "github.com/theupdateframework/notary/tuf/data"
)

func init() {
	initCmd := &cobra.Command{
		Use:   "init <wave> <version> <tag>",
		Short: "Create a new wave from targets of a given version",
		Long: `Create a new wave from targets of a given version.

This command only initializes a wave, but does not provision its updates to devices.
Use a "fioctl wave rollout <wave> <group>" to trigger updates of this wave to a device group.
Use a "fioctl wave complete <wave>" to update all devices (make it globally available).
Use a "fioctl wave cancel <wave> to cancel a wave (make it no longer available).`,
		Run:  doInitWave,
		Args: cobra.ExactArgs(3),
	}
	cmd.AddCommand(initCmd)
	initCmd.Flags().IntP("expires-days", "e", 0, `Role expiration in days; default 365.
The same expiration will be used for production targets when a wave is complete.`)
	initCmd.Flags().StringP("expires-at", "E", "", `Role expiration date and time in RFC 3339 format.
The same expiration will be used for production targets when a wave is complete.
When set this value overrides an 'expires-days' argument.
Example: 2020-01-01T00:00:00Z`)
	initCmd.Flags().BoolP("edit", "", false, "Manually edit targets list before signing. DANGER - EXPERT MODE")
	initCmd.Flags().BoolP("dry-run", "d", false, "Don't create a wave, print it to standard output.")
	initCmd.Flags().StringP("keys", "k", "", "Path to <offline-creds.tgz> used to sign wave targets.")
	initCmd.Flags().StringP("source-tag", "", "", "Match this tag when looking for target versions. Certain advanced tagging configurations may require this argument.")
	_ = initCmd.MarkFlagRequired("keys")
	_ = initCmd.Flags().MarkHidden("edit")
}

func doInitWave(cmd *cobra.Command, args []string) {
	factory := viper.GetString("factory")
	name, version, tag := args[0], args[1], args[2]
	intVersion, err := strconv.ParseInt(version, 10, 32)
	subcommands.DieNotNil(err, "Version must be an integer")
	edit, _ := cmd.Flags().GetBool("edit")
	expires := readExpiration(cmd)
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	sourceTag, _ := cmd.Flags().GetString("source-tag")
	offlineKeys := readOfflineKeys(cmd)
	logrus.Debugf("Creating a wave %s for factory %s targets version %s and new tag %s expires %s",
		name, factory, version, tag, expires.Format(time.RFC3339))

	new_targets, err := api.TargetsList(factory, version)
	subcommands.DieNotNil(err)
	if len(new_targets) == 0 {
		subcommands.DieNotNil(fmt.Errorf("No targets found for version %s", version))
	}

	current_targets, err := api.ProdTargetsGet(factory, tag, false)
	subcommands.DieNotNil(err)

	targets := client.AtsTargetsMeta{}
	targets.Type = tuf.TUFTypes["targets"]
	targets.Expires = expires
	targets.Version = int(intVersion)
	if current_targets == nil {
		targets.Targets = make(tuf.Files)
	} else {
		targets.Targets = current_targets.Signed.Targets
		if targets.Version <= current_targets.Signed.Version {
			subcommands.DieNotNil(fmt.Errorf(
				"Cannot create a wave for a version lower than production targets for the same tag"))
		}
	}

	for name, file := range new_targets {
		if _, exists := targets.Targets[name]; exists {
			subcommands.DieNotNil(fmt.Errorf("Target %s already exists in production targets for tag %s", name, tag))
		}

		if len(sourceTag) > 0 {
			custom, err := api.TargetCustom(file)
			subcommands.DieNotNil(err)
			found := false
			for _, tag := range custom.Tags {
				if tag == sourceTag {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		subcommands.DieNotNil(replaceTags(&file, tag), fmt.Sprintf("Malformed CI target custom field %s", name))
		targets.Targets[name] = file
	}

	if edit {
		targets = editTargets(targets)
	}

	meta, err := canonical.MarshalCanonical(targets)
	subcommands.DieNotNil(err, "Failed to serialize new targets")
	signatures := signTargets(meta, factory, offlineKeys)

	signed := tuf.Signed{
		// Existing signatures are invalidated by new targets, throw them away.
		Signatures: signatures,
		Signed:     &canonical.RawMessage{},
	}
	_ = signed.Signed.UnmarshalJSON(meta)

	wave := client.WaveCreate{
		Name:    name,
		Version: version,
		Tag:     tag,
		Targets: signed,
	}
	if dryRun {
		payload, err := json.MarshalIndent(&wave, "", "  ")
		subcommands.DieNotNil(err, "Failed to marshal a wave")
		fmt.Println(string(payload))
	} else {
		subcommands.DieNotNil(api.FactoryCreateWave(factory, &wave), "Failed to create a wave")
	}
}

func editTargets(targets client.AtsTargetsMeta) client.AtsTargetsMeta {
	indentedMeta, err := json.MarshalIndent(targets, "", "  ")
	subcommands.DieNotNil(err, "Failed to serialize new targets")

	// Create temp file to edit with
	tmpfile, err := ioutil.TempFile("", "targets.*.json")
	if err != nil {
		fmt.Println("Unable to create tempfile: ", err)
		os.Exit(1)
	}
	defer os.Remove(tmpfile.Name())
	if _, err := tmpfile.Write(indentedMeta); err != nil {
		fmt.Println("Unable to write tempfile: ", err)
		os.Exit(1)
	}
	if err := tmpfile.Close(); err != nil {
		fmt.Println("Unable to close tempfile: ", err)
		os.Exit(1)
	}

	// Let user edit the file
	editor := os.Getenv("EDITOR")
	if len(editor) == 0 {
		editor = "/usr/bin/vi"
	}
	edit := exec.Command(editor, tmpfile.Name())
	edit.Stdout = os.Stdout
	edit.Stderr = os.Stderr
	edit.Stdin = os.Stdin
	logrus.Debug("Running editor and waiting for it to finish...")
	if err := edit.Run(); err != nil {
		fmt.Println("Editing cancelled: ", err)
		os.Exit(0)
	}

	// Read it and see if its changed
	content, err := ioutil.ReadFile(tmpfile.Name())
	if err != nil {
		fmt.Println("ERROR: Unable to re-read tempfile:", err)
	}
	if bytes.Equal(content, indentedMeta) {
		fmt.Println("No changes found, exiting.")
		os.Exit(0)
	}
	var newTargets client.AtsTargetsMeta
	err = json.Unmarshal(content, &newTargets)
	subcommands.DieNotNil(err, "Unable to parse the editted targets")
	return newTargets
}

func signTargets(meta []byte, factory string, offlineKeys keys.OfflineCreds) []tuf.Signature {
	root, err := api.TufRootGet(factory)
	subcommands.DieNotNil(err, "Failed to fetch root role")
	onlinePub, err := api.GetFoundriesTargetsKey(factory)
	subcommands.DieNotNil(err, "Failed to fetch online targets public key")

	signers := make([]keys.TufSigner, 0)
	for _, kid := range root.Signed.Roles["targets"].KeyIDs {
		pub := root.Signed.Keys[kid].KeyValue.Public
		if pub == onlinePub.KeyValue.Public {
			continue
		}
		signer, err := keys.FindSigner(kid, pub, offlineKeys)
		if err != nil {
			subcommands.DieNotNil(err, fmt.Sprintf("Failed to find private key for %s", kid))
		}
		signers = append(signers, *signer)
	}

	if len(signers) == 0 {
		subcommands.DieNotNil(fmt.Errorf(`Root role is not configured to sign targets offline.
Please, run "fioctl keys rotate-targets" in order to create offline targets keys.`))
	}

	signatures, err := keys.SignMeta(meta, signers...)
	subcommands.DieNotNil(err, "Failed to sign new targets")
	return signatures
}

func readExpiration(cmd *cobra.Command) (expires time.Time) {
	if cmd.Flags().Changed("expires-at") {
		at, _ := cmd.Flags().GetString("expires-at")
		expires, err := time.Parse(time.RFC3339, at)
		subcommands.DieNotNil(err, "Invalid expires-at value:")
		expires = expires.UTC()
	} else {
		days := 365
		if cmd.Flags().Changed("expires-days") {
			days, _ = cmd.Flags().GetInt("expires-days")
		}
		expires = time.Now().UTC().Add(time.Duration(days*24) * time.Hour)
	}
	// This forces a JSON marshaller to use an RFC3339 instead of the default RFC3339Nano format.
	// An aktualizr we use on devices to update targets doesn't understand the latter one.
	return expires.Round(time.Second)
}

func readOfflineKeys(cmd *cobra.Command) keys.OfflineCreds {
	offlineKeysFile, _ := cmd.Flags().GetString("keys")
	offlineKeys, err := keys.GetOfflineCreds(offlineKeysFile)
	subcommands.DieNotNil(err, "Failed to open offline keys file")
	return offlineKeys
}

func replaceTags(target *tuf.FileMeta, tag string) error {
	// A client.TufCustom isn't suitable here, as a target might have other "non-standard" fields not
	// covered by this struct.  We still need to preserve all the original fields except for tags.
	var custom map[string]interface{}
	if err := json.Unmarshal(*target.Custom, &custom); err != nil {
		return err
	}
	// We don't care what tags are there, but we know what tags we want to be there
	custom["tags"] = []string{tag}
	if data, err := canonical.MarshalCanonical(custom); err != nil {
		return err
	} else {
		return target.Custom.UnmarshalJSON(data)
	}
}
