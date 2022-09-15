package clearingway

import (
	"fmt"
	"strings"
	"time"

	"github.com/Veraticus/clearingway/internal/discord"
	"github.com/Veraticus/clearingway/internal/fflogs"
	"github.com/Veraticus/clearingway/internal/ffxiv"

	"github.com/bwmarrin/discordgo"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

func (c *Clearingway) DiscordReady(s *discordgo.Session, event *discordgo.Ready) {
	fmt.Printf("Initializing Discord...\n")

	for _, discordGuild := range event.Guilds {
		gid := discordGuild.ID
		guild, ok := c.Guilds.Guilds[discordGuild.ID]
		if !ok {
			panic(fmt.Sprintf("Initialized in guild %s with no configuration!", gid))
		}
		existingRoles, err := s.GuildRoles(gid)
		if err != nil {
			fmt.Printf("Error getting existing roles: %v\n", err)
			return
		}

		fmt.Printf("Initializing roles...\n")
		guild.EncounterRoles = guild.Encounters.Roles()
		err = guild.EncounterRoles.Ensure(gid, s, existingRoles)
		if err != nil {
			fmt.Printf("Error ensuring encounter roles: %v", err)
		}

		guild.ParsingRoles = ParsingRoles()
		err = guild.ParsingRoles.Ensure(gid, s, existingRoles)
		if err != nil {
			fmt.Printf("Error ensuring parsing roles: %v", err)
		}

		guild.UltimateRoles = UltimateRoles()
		err = guild.UltimateRoles.Ensure(gid, s, existingRoles)
		if err != nil {
			fmt.Printf("Error ensuring ultimate roles: %v", err)
		}

		guild.WorldRoles = WorldRoles()
		err = guild.WorldRoles.Ensure(gid, s, existingRoles)
		if err != nil {
			fmt.Printf("Error ensuring world roles: %v", err)
		}

		fmt.Printf("Adding commands...\n")
		_, err = s.ApplicationCommandCreate(event.User.ID, discordGuild.ID, ClearCommand)
		if err != nil {
			fmt.Printf("Could not add command: %v\n", err)
		}

		// fmt.Printf("Removing commands...\n")
		// cmd, err := s.ApplicationCommandCreate(event.User.ID, guild.ID, verifyCommand)
		// if err != nil {
		// 	fmt.Printf("Could not find command: %v\n", err)
		// }
		// err = s.ApplicationCommandDelete(event.User.ID, guild.ID, cmd.ID)
		// if err != nil {
		// 	fmt.Printf("Could not delete command: %v\n", err)
		// }
	}
	fmt.Printf("Clearingway ready!\n")
}

var ClearCommand = &discordgo.ApplicationCommand{
	Name:        "clears",
	Description: "Verify you own your character and assign them cleared roles.",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionString,
			Name:        "world",
			Description: "Your character's world",
			Required:    true,
		},
		{
			Type:        discordgo.ApplicationCommandOptionString,
			Name:        "first-name",
			Description: "Your character's first name",
			Required:    true,
		},
		{
			Type:        discordgo.ApplicationCommandOptionString,
			Name:        "last-name",
			Description: "Your character's last name",
			Required:    true,
		},
	},
}

func (c *Clearingway) InteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	g, ok := c.Guilds.Guilds[i.GuildID]
	if !ok {
		fmt.Printf("Interaction received from guild %s with no configuration!\n", i.GuildID)
	}

	// Ignore messages not on the correct channel
	if i.ChannelID != g.ChannelId {
		fmt.Printf("Ignoring message not in channel %s.\n", g.ChannelId)
	}

	// Retrieve all the options sent to the command
	options := i.ApplicationCommandData().Options
	optionMap := make(map[string]*discordgo.ApplicationCommandInteractionDataOption, len(options))
	for _, opt := range options {
		optionMap[opt.Name] = opt
	}

	var world string
	var firstName string
	var lastName string

	if option, ok := optionMap["world"]; ok {
		world = option.StringValue()
	}
	if option, ok := optionMap["first-name"]; ok {
		firstName = option.StringValue()
	}
	if option, ok := optionMap["last-name"]; ok {
		lastName = option.StringValue()
	}

	title := cases.Title(language.AmericanEnglish)
	world = title.String(world)
	firstName = title.String(firstName)
	lastName = title.String(lastName)

	if len(world) == 0 || len(firstName) == 0 || len(lastName) == 0 {
		err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "`/clears` command failed! Make sure you input your world, first name, and last name.",
			},
		})
		if err != nil {
			fmt.Printf("Error sending Discord message: %v", err)
		}
		return
	}

	err := discord.StartInteraction(s, i.Interaction,
		fmt.Sprintf("Finding `%s %s (%s)` in the Lodestone...", firstName, lastName, world),
	)
	if err != nil {
		fmt.Printf("Error sending Discord message: %v", err)
		return
	}

	char, err := g.Characters.Init(world, firstName, lastName)
	if err != nil {
		err = discord.ContinueInteraction(s, i.Interaction, err.Error())
		if err != nil {
			fmt.Printf("Error sending Discord message: %v", err)
		}
		return
	}

	err = discord.ContinueInteraction(s, i.Interaction,
		fmt.Sprintf("Verifying ownership of `%s (%s)`...", char.Name(), char.World),
	)
	if err != nil {
		fmt.Printf("Error sending Discord message: %v", err)
	}

	discordId := i.Member.User.ID
	isOwner, err := char.IsOwner(discordId)
	if err != nil {
		err = discord.ContinueInteraction(s, i.Interaction, err.Error())
		if err != nil {
			fmt.Printf("Error sending Discord message: %v", err)
		}
		return
	}
	if !isOwner {
		discord.ContinueInteraction(s, i.Interaction,
			fmt.Sprintf(
				"I could not verify your ownership of `%s (%s)`!\nIf this is your character, add the following code to your Lodestone profile and then run `/clears` again:\n\n**%s**\n\nYou can edit your Lodestone profile at https://na.finalfantasyxiv.com/lodestone/my/setting/profile/",
				char.Name(),
				char.World,
				char.LodestoneSlug(discordId),
			),
		)
		if err != nil {
			fmt.Printf("Error sending Discord message: %v", err)
		}
		return
	}

	err = discord.ContinueInteraction(s, i.Interaction,
		fmt.Sprintf("Analyzing logs for `%s (%s)`...", char.Name(), char.World),
	)
	if err != nil {
		fmt.Printf("Error sending Discord message: %v", err)
	}

	if char.UpdatedRecently() {
		err = discord.ContinueInteraction(s, i.Interaction,
			fmt.Sprintf("Finished analysis for `%s (%s)`.", char.Name(), char.World),
		)
		if err != nil {
			fmt.Printf("Error sending Discord message: %v", err)
		}
		return
	}

	charText, err := c.UpdateCharacterInGuild(char, i.Member.User.ID, g)
	if err != nil {
		err = discord.ContinueInteraction(s, i.Interaction,
			fmt.Sprintf("Could not analyze clears for `%s (%s)`: %s", char.Name(), char.World, err),
		)
		return
	}

	err = discord.ContinueInteraction(s, i.Interaction,
		fmt.Sprintf("Finished analysis for `%s (%s)`.\n%s", char.Name(), char.World, charText),
	)
	if err != nil {
		fmt.Printf("Error sending Discord message: %v", err)
	}
	return
}

func (c *Clearingway) UpdateCharacterInGuild(char *ffxiv.Character, discordUserId string, guild *Guild) (string, error) {
	rankingsToGet := []*fflogs.RankingToGet{}
	for _, encounter := range guild.AllEncounters() {
		rankingsToGet = append(rankingsToGet, &fflogs.RankingToGet{IDs: encounter.Ids, Difficulty: encounter.DifficultyInt()})
	}
	rankings, err := c.Fflogs.GetRankingsForCharacter(rankingsToGet, char)
	if err != nil {
		return "", fmt.Errorf("Error retrieving encounter rankings: %w", err)
	}

	member, err := c.Discord.Session.GuildMember(guild.Id, discordUserId)
	if err != nil {
		return "", fmt.Errorf("Could not retrieve roles for user: %w", err)
	}
	fmt.Printf("Got member: %+v\n", member)
	fmt.Printf("Roles are: %+v\n", member.Roles)

	text := strings.Builder{}
	text.WriteString("")

	shouldApplyOpts := &ShouldApplyOpts{
		Character: char,
		Rankings:  rankings,
	}

	rolesToApply := []*Role{}
	rolesToRemove := []*Role{}

	// Do not include ultimate encounters for encounter, parsing,
	// and world roles, since we don't want clears for those fight
	// to count towards clears or colors.
	for _, role := range guild.NonUltRoles() {
		if role.ShouldApply == nil {
			continue
		}

		shouldApplyOpts.Encounters = guild.Encounters

		shouldApply := role.ShouldApply(shouldApplyOpts)
		if shouldApply {
			rolesToApply = append(rolesToApply, role)
		} else {
			rolesToRemove = append(rolesToRemove, role)
		}
	}

	// Add ultimate roles too
	for _, role := range guild.UltimateRoles.Roles {
		if role.ShouldApply == nil {
			continue
		}

		shouldApplyOpts.Encounters = UltimateEncounters

		shouldApply := role.ShouldApply(shouldApplyOpts)
		if shouldApply {
			rolesToApply = append(rolesToApply, role)
		} else {
			rolesToRemove = append(rolesToRemove, role)
		}
	}

	for _, role := range rolesToApply {
		if !role.PresentInRoles(member.Roles) {
			err := role.AddToCharacter(guild.Id, discordUserId, c.Discord.Session, char)
			if err != nil {
				return "", fmt.Errorf("Error adding Discord role: %v", err)
			}
			text.WriteString(fmt.Sprintf("Adding role: `%s`\n", role.Name))
		}
	}

	for _, role := range rolesToRemove {
		if role.PresentInRoles(member.Roles) {
			err := role.RemoveFromCharacter(guild.Id, discordUserId, c.Discord.Session, char)
			if err != nil {
				return "", fmt.Errorf("Error removing Discord role: %v", err)
			}
			text.WriteString(fmt.Sprintf("Removing role: `%s`\n", role.Name))
		}
	}

	char.LastUpdateTime = time.Now()

	return text.String(), nil
}