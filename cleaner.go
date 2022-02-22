package main

import (
	"context"
	"fmt"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"github.com/xanzy/go-gitlab"
	"os"
)

var desiredStatus = "offline"
var listOptions gitlab.ListOptions

func main() {
	fmt.Println("cleaning")
	viper.AutomaticEnv()
	viper.SetDefault("PAGE_SIZE", 50)
	viper.SetDefault("DELETE_PERSONAL", true)
	viper.SetDefault("DELETE_GROUP_RUNNERS", false)
	viper.SetDefault("GROUP_IDS", []string{})

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	listOptions = gitlab.ListOptions{
		PerPage: viper.GetInt("PAGE_SIZE"),
	}

	token := viper.GetString("GITLAB_TOKEN")
	if token == "" {
		log.Fatal().Msg("you need to set `GITLAB_TOKEN` env var")
	}

	log.Debug().Msg("Connecting to gitlab...\n")
	gl, err := gitlab.NewClient(token)
	if err != nil {
		log.Fatal().Err(err).Msg("cannot connect to gitlab server")
	}

	//delete personal runners
	ctx := context.Background()
	if viper.GetBool("DELETE_PERSONAL") {
		_ = runPersonalRunners(ctx, gl)
	}
	if viper.GetBool("DELETE_GROUP_RUNNERS") {
		groupIds := viper.GetStringSlice("GROUP_IDS")
		if len(groupIds) == 0 {
			log.Fatal().
				Msg("group runner deletion has been required, but no group ids has been set. Check GROUP_IDS var")
		}
		for _, id := range groupIds {
			logger := log.With().
				Str("group_id", id).
				Logger()
			_ = runGroupRunners(logger.WithContext(ctx), gl, id)
		}
	}

}
func runGroupRunners(ctx context.Context, gl *gitlab.Client, groupID string) error {
	var totalRunnerList []*gitlab.Runner
	logger := log.Ctx(ctx)
	logger.Debug().Msg("collecting runners")
	//due to gitlab putting shared runners in, we have to go through this double fetching way
	for {
		listOptions.Page = -1
		for {
			listOptions.Page++
			runnerList, resp, err := gl.Runners.ListGroupsRunners(groupID, &gitlab.ListGroupsRunnersOptions{
				ListOptions: listOptions,
				Status:      &desiredStatus,
			})
			switch {
			case err != nil:
				logger.Err(err).Msg("cannot list group runners")
				return err
			case len(runnerList) == 0:
				break
			}
			logger.Trace().
				Int("total_results", resp.TotalItems).
				Int("page", listOptions.Page).
				Msg("got group runners")

			for k, r := range runnerList {
				if r.IsShared {
					logger.Trace().
						Int("id", r.ID).
						Str("runner_name", r.Name).
						Str("runner_description", r.Description).
						Msg("runner is shared, skipping")
					continue
				}
				totalRunnerList = append(totalRunnerList, runnerList[k])
			}
			// if we got to the end of the list, break now
			if listOptions.Page+1 >= resp.TotalPages {
				logger.Trace().Msg("reached the end of the list")
				break
			}
		}

		//goland:noinspection GoUnreachableCode
		return deleteRunnersFromResults(logger.WithContext(ctx), gl, totalRunnerList)
	}
	//goland:noinspection ALL
	return nil
}

func runPersonalRunners(ctx context.Context, gl *gitlab.Client) error {
	for {
		runnerList, resp, err := gl.Runners.ListRunners(&gitlab.ListRunnersOptions{
			ListOptions: listOptions,
			//Status:      &desiredStatus,
		})
		logger := log.With().
			Int("count", resp.TotalItems).
			Int("page_size", listOptions.Page).
			Logger()

		switch {
		case err != nil:
			logger.Err(err).Msg("cannot list runners")
			return err
		case resp.TotalItems == 0 || len(runnerList) == 0:
			logger.Debug().Msg("no offline runners found")
			return nil
		}

		err = deleteRunnersFromResults(log.Logger.WithContext(ctx), gl, runnerList)
		if err != nil {
			return err
		}
	}
	//goland:noinspection ALL
	return nil
}

func deleteRunnersFromResults(ctx context.Context, gl *gitlab.Client, runnerList []*gitlab.Runner) error {
	var err error
	for _, r := range runnerList {
		logger := log.Ctx(ctx).With().
			Str("runner_type", r.RunnerType).
			Str("runner_name", r.Name).
			Str("runner_descriptions", r.Description).
			Int("runner_id", r.ID).
			Logger()

		switch {
		case viper.GetBool("DRY_RUN"):
			logger.Debug().Msg("dry-run: deleting")
			continue
		case r.Online:
			logger.Trace().Msg("runner is online, skipping")
			continue
		case r.IsShared:
			logger.Trace().Msg("runner is shared, skipping")
			continue
		case r.Status == "not_connected":
			logger.Trace().Msg("runner status is no_connected, deleting")
			_, err = gl.Runners.DeleteRegisteredRunnerByID(r.ID)
		default:
			logger.Debug().Msg("deleting runner")
			_, err = gl.Runners.DeleteRegisteredRunnerByID(r.ID)
		}
		if err != nil {
			logger.Err(err).Msg("cannot delete runner")
			return err
		}
	}
	return nil
}
