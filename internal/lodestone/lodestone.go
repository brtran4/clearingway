package lodestone

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Veraticus/clearingway/internal/ffxiv"

	"github.com/gocolly/colly"
)

var lodestoneUrl = "https://na.finalfantasyxiv.com/lodestone"

func SetCharacterLodestoneID(c *ffxiv.Character) error {
	if c.LodestoneID != 0 {
		return nil
	}
	fmt.Printf("Lodestone ID not set for %s (%s), checking the Lodestone...\n", c.Name(), c.World)

	collector := colly.NewCollector(colly.Async(true))
	collector.SetRequestTimeout(30 * time.Second)
	charIDs := []int{}
	errors := []error{}
	spawnedChildren := false
	searchUrl := fmt.Sprintf(
		"/character/?q=%v&worldname=%v",
		url.QueryEscape(c.Name()),
		c.World,
	)

	collector.OnHTML(".ldst__window .entry", func(e *colly.HTMLElement) {
		name := e.ChildText(".entry__name")
		if strings.EqualFold(name, c.Name()) {
			linkText := e.ChildAttr(".entry__link", "href")
			var charID int
			n, err := fmt.Sscanf(linkText, "/lodestone/character/%d/", &charID)
			if n == 0 {
				errors = append(errors, fmt.Errorf("Could not find character ID!"))
			}
			if err != nil {
				errors = append(errors, fmt.Errorf("Could not parse lodestone URL: %w", err))
			}
			charIDs = append(charIDs, charID)
		}
	})

	collector.OnHTML(".ldst__window ul.btn__pager", func(e *colly.HTMLElement) {
		var currentPage int
		var maxPages int
		pages := e.ChildText(".btn__pager__current")
		n, err := fmt.Sscanf(pages, "Page %d of %d", &currentPage, &maxPages)
		if n == 0 {
			errors = append(errors, fmt.Errorf("Could not find pager!"))
		}
		if err != nil {
			errors = append(errors, fmt.Errorf("Could not parse pager: %w", err))
		}
		if !spawnedChildren && currentPage == 1 && maxPages != 1 {
			spawnedChildren = true
			for i := 2; i <= maxPages; i++ {
				err = e.Request.Visit(lodestoneUrl + searchUrl + fmt.Sprintf("&page=%d", i))
				if err != nil {
					errors = append(errors, fmt.Errorf("Could not spawn child page: %w", err))
				}
			}
		}
	})

	collector.OnError(func(resp *colly.Response, err error) {
		errors = append(errors, err)
	})

	err := collector.Visit(lodestoneUrl + searchUrl)
	if err != nil {
		return fmt.Errorf("Could not visit Lodestone: %w", err)
	}
	collector.Wait()

	if len(errors) != 0 {
		return buildError(errors)
	}

	if len(charIDs) == 0 {
		return fmt.Errorf(
			"No character found on the Lodestone for `%v (%v)`! If you recently renamed yourself or server transferred it can take up to a day for this to be reflected on the Lodestone; please try again later.",
			c.Name(),
			c.World,
		)
	}
	if len(charIDs) > 1 {
		return fmt.Errorf(
			"Too many characters found for name %s (%s)! Ensure it is exactly your character name.\nAlternatively, import your character to FFlogs at https://www.fflogs.com/lodestone/import to circumvent a Lodestone search.",
			c.Name(),
			c.World,
		)
	}

	c.LodestoneID = charIDs[0]

	return nil
}

func CharacterIsOwnedByDiscordUser(c *ffxiv.Character, discordId string) (bool, error) {
	collector := colly.NewCollector(colly.Async(true))
	collector.SetRequestTimeout(30 * time.Second)
	errors := []error{}
	bio := ""

	collector.OnHTML(".character__content.selected", func(e *colly.HTMLElement) {
		bio = e.ChildText(".character__selfintroduction")
	})

	collector.OnError(func(resp *colly.Response, err error) {
		errors = append(errors, err)
	})

	err := collector.Visit(lodestoneUrl + fmt.Sprintf("/character/%d/", c.LodestoneID))
	if err != nil {
		return false, fmt.Errorf("Could not visit Lodestone: %w", err)
	}
	collector.Wait()

	if len(errors) != 0 {
		return false, buildError(errors)
	}

	if !strings.Contains(bio, c.LodestoneSlug(discordId)) {
		return false, nil
	}

	return true, nil
}

func buildError(errors []error) error {
	errorText := strings.Builder{}
	for _, e := range errors {
		errorText.WriteString(e.Error() + "\n")
	}
	return fmt.Errorf("Encountered search errors:\n%v", errorText.String())
}
