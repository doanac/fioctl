package users

import (
	"strings"

	"github.com/cheynewallace/tabby"
	"github.com/foundriesio/fioctl/client"
	"github.com/foundriesio/fioctl/subcommands"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var whoami bool

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "users [<user_id>]",
		Short: "List users with access to a FoundriesFactory",
		Args:  cobra.RangeArgs(0, 1),
		Run:   doUserCommand,
	}
	subcommands.RequireFactory(cmd)
	cmd.Flags().BoolVar(&whoami, "whoami", false, "Look up the user information found for your local Fioctl credentials")
	return cmd
}

func doUserCommand(cmd *cobra.Command, args []string) {
	if whoami {
		doWhoAmI(subcommands.Login(cmd))
	} else if len(args) == 0 {
		doList(subcommands.Login(cmd), viper.GetString("factory"))
	} else {
		doGetUser(subcommands.Login(cmd), viper.GetString("factory"), args[0])
	}

}

func doList(api *client.Api, factory string) {
	logrus.Debugf("Listing factory users for %s", factory)

	users, err := api.UsersList(factory)
	subcommands.DieNotNil(err)

	t := tabby.New()
	t.AddHeader("ID", "NAME", "ROLE")
	for _, user := range users {
		t.AddLine(user.PolisId, user.Name, user.Role)
	}
	t.Print()
}

func doGetUser(api *client.Api, factory, user_id string) {
	user, err := api.UserAccessDetails(factory, user_id)
	subcommands.DieNotNil(err)
	t := tabby.New()
	t.AddHeader("ID", "NAME", "ROLE")
	t.AddLine(user.PolisId, user.Name, user.Role)

	t.AddLine()
	t.AddHeader("TEAMS")
	for _, team := range user.Teams {
		t.AddLine(team.Name)
		var scopes []string
		for _, s := range team.Scopes {
			scopes = append(scopes, s[strings.Index(s, ":")+1:])
		}
		if len(scopes) > 0 {
			t.AddLine("\tScopes: " + strings.Join(scopes, ", "))
		}
		if len(team.Groups) > 0 {
			t.AddLine("\tGroups: " + strings.Join(team.Groups, ", "))
		}
		t.AddLine()
	}
	t.AddLine()
	t.AddHeader("EFFECTIVE SCOPES")
	for _, scope := range user.EffectiveScopes {
		t.AddLine(scope[strings.Index(scope, ":")+1:])
	}
	t.Print()
}

func doWhoAmI(api *client.Api) {
	user, err := api.WhoAmI()
	subcommands.DieNotNil(err)

	t := tabby.New()
	t.AddHeader("ID")
	t.AddLine(user.PolisId)

	t.AddLine()
	t.AddHeader("User belongs to factories:")
	for _, team := range user.Teams {
		t.AddLine(team)
	}

	t.AddLine()
	t.AddHeader("Scopes you've configured for this token:")
	for _, scope := range user.Scopes {
		t.AddLine(scope)
	}

	allowed := make(map[string]bool)

	t.AddLine()
	t.AddHeader("Allowed scopes set by your Factory admin")
	for _, scopes := range user.AllowedScopes {
		for _, scope := range scopes {
			allowed[scope] = true
			// they also have "read" support
			if strings.HasSuffix(scope, "-update") {
				allowed[scope[0:len(scope)-7]] = true
			}
			t.AddLine(scope)
		}
	}

	t.AddLine()
	t.AddHeader("Effective access (the intersection of allowed scopes and your scopes)")
	for _, scope := range user.Scopes {
		// check for:
		// * exact scope match
		// * allowed scope is read-update, scope is read. Effective is read
		// * allowed scope is read, scope is read-update. Effective is read
		if _, ok := allowed[scope]; ok {
			t.AddLine(scope)
		} else {
			if strings.HasSuffix(scope, "read-update") {
				roscope := scope[0 : len(scope)-7]
				if _, ok := allowed[roscope]; ok {
					// we hve read-update scope but are given read-only access
					t.AddLine(roscope)
				}
			} else if strings.HasSuffix(scope, "read") {
				rwscope := scope + "-update"
				if _, ok := allowed[rwscope]; ok {
					// we hve read-only scope but are given read-write access
					t.AddLine(scope)
				}
			}
		}
	}
}
