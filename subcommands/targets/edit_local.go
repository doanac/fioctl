package targets

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	canonical "github.com/tent/canonical-json-go"

	"github.com/foundriesio/fioctl/client"
	"github.com/foundriesio/fioctl/subcommands"
	"github.com/foundriesio/fioctl/subcommands/keys"
)

var editDryRun bool

func init() {
	editCmd := &cobra.Command{
		Use:    "edit-local <offline-keys.tgz>",
		Short:  "Edit/sign targets.json directly - proceed with caution!",
		Run:    doEditLocal,
		Hidden: true,
		Args:   cobra.ExactArgs(1),
	}
	editCmd.Flags().BoolVarP(&editDryRun, "dryrun", "", false, "Sign locally, don't push results")
	cmd.AddCommand(editCmd)
}

func doEditLocal(cmd *cobra.Command, args []string) {
	factory := viper.GetString("factory")
	logrus.Debugf("Editing targets for %s", factory)

	signer := getTargetSigner(factory, args[0])

	targets, checksum := getAtsTargets(factory)
	expectedVersion := targets.Version + 1
	targets.Version = expectedVersion
	orig, err := json.MarshalIndent(targets, "", "  ")
	subcommands.DieNotNil(err)

	// Create temp file to edit with
	tmpfile, err := ioutil.TempFile("", "targets.*.json")
	subcommands.DieNotNil(err)
	defer os.Remove(tmpfile.Name())

	_, err = tmpfile.Write(orig)
	subcommands.DieNotNil(err)

	err = tmpfile.Close()
	subcommands.DieNotNil(err)

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
	subcommands.DieNotNil(err)
	if bytes.Equal(content, orig) {
		fmt.Println("No changes found, exiting.")
		os.Exit(0)
	}

	// Sign changes
	var newTargets client.AtsTargetsMeta
	err = json.Unmarshal(content, &newTargets)
	subcommands.DieNotNil(err)

	if newTargets.Version != expectedVersion {
		subcommands.DieNotNil(fmt.Errorf("New targets version must be %d not %d", expectedVersion, newTargets.Version))
	}

	bytes, err := canonical.Marshal(newTargets)
	subcommands.DieNotNil(err)
	signatures, err := keys.SignMeta(bytes, signer)
	subcommands.DieNotNil(err)

	signedTargets := client.AtsTufTargets{
		Signatures: signatures,
		Signed:     newTargets,
	}
	if editDryRun {
		data, err := json.MarshalIndent(newTargets, "", "  ")
		subcommands.DieNotNil(err)
		fmt.Println(string(data))
	} else {
		subcommands.DieNotNil(api.TargetsPost(factory, checksum, signedTargets))
	}
}

func getAtsTargets(factory string) (client.AtsTargetsMeta, string) {
	raw, checksum, err := api.TargetsListRaw(factory)
	subcommands.DieNotNil(err)
	var targets client.AtsTufTargets
	err = json.Unmarshal(*raw, &targets)
	subcommands.DieNotNil(err)

	return targets.Signed, checksum
}

func getTargetSigner(factory, credsFile string) keys.TufSigner {
	creds, err := keys.GetOfflineCreds(credsFile)
	subcommands.DieNotNil(err)

	root, err := api.TufRootGet(factory)
	subcommands.DieNotNil(err)

	onlineTargetId, err := keys.FindOnlineTargetId(api, factory, *root, creds)
	subcommands.DieNotNil(err)

	for _, kid := range root.Signed.Roles["targets"].KeyIDs {
		if kid == onlineTargetId {
			continue
		}
		pub := root.Signed.Keys[kid].KeyValue.Public
		pkey, err := keys.FindPrivKey(pub, creds)
		if err != nil {
			subcommands.DieNotNil(fmt.Errorf("Failed to find private key for %s: %w", kid, err))
		}
		fmt.Println("Signing with key:", kid)
		return keys.TufSigner{Id: kid, Key: pkey}
	}
	subcommands.DieNotNil(errors.New("Unable to find offline target signing key"))
	return keys.TufSigner{} // make compiler happy
}
