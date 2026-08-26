package app

import (
	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/installstate"
	githubsource "github.com/alx4j/ai4j/internal/source/github"
)

func updateSourceOptions(record installstate.Record) (cli.SourceOptions, error) {
	if record.Source.Mode == "development_source" {
		return cli.NewDevelopmentSourceOptions(record.Source.Checkout, false)
	}
	repository, repositoryProvided, err := storedSourceRepository(record)
	if err != nil {
		return cli.SourceOptions{}, err
	}
	if record.Source.RequestedRef == nil {
		return cli.NewSourceOptions(repository, repositoryProvided, "", false)
	}
	return cli.NewSourceOptions(repository, repositoryProvided, *record.Source.RequestedRef, true)
}

func exactSourceOptions(record installstate.Record) (cli.SourceOptions, error) {
	if record.Source.Mode == "development_source" {
		return cli.NewDevelopmentSourceOptions(record.Source.Checkout, false)
	}
	repository, repositoryProvided, err := storedSourceRepository(record)
	if err != nil {
		return cli.SourceOptions{}, err
	}
	return cli.NewSourceOptions(repository, repositoryProvided, record.Source.Commit, true)
}

func storedSourceRepository(record installstate.Record) (string, bool, error) {
	if record.Source.Selection == domain.BuiltInDefaultSource().String() {
		return "", false, nil
	}
	identity, err := domain.NewRepositoryIdentity(record.Source.Repository)
	if err != nil {
		return "", false, err
	}
	transport := domain.HTTPSGitTransport()
	if record.Source.Transport != "" {
		transport, err = domain.NewGitTransport(record.Source.Transport)
		if err != nil {
			return "", false, err
		}
	}
	remote, err := githubsource.ReconstructRemote(identity, transport)
	if err != nil {
		return "", false, err
	}
	return remote.Endpoint(), true, nil
}
