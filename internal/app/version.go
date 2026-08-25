package app

import (
	"fmt"

	"github.com/alx4j/ai4j/internal/buildinfo"
	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/result"
)

type versionHandler struct {
	build         buildinfo.Info
	defaultSource cli.DefaultSource
}

func (h versionHandler) Handle(request cli.Request) (cli.Response, error) {
	if request == nil || request.Command() != cli.CommandVersion {
		return cli.Response{}, fmt.Errorf("version handler requires a version request")
	}
	data, err := h.versionData()
	if err != nil {
		return newUnavailableResponse(cli.CommandVersion, "version_data_unavailable", "version information is unavailable")
	}
	commandResult, err := neutralResult(result.StatusOK, result.FailureNone, nil)
	if err != nil {
		return cli.Response{}, err
	}
	return cli.NewResponse(cli.CommandVersion, commandResult, nil, data)
}

func (h versionHandler) versionData() (cli.VersionData, error) {
	if !h.build.VCSAvailable() || h.build.VCS() != "git" || h.build.MainModule() != buildinfo.Module || h.build.PackagePath() != buildinfo.CommandPackage || h.build.BuildTime().Year() < 1 || h.build.BuildTime().Year() > 9999 {
		return cli.VersionData{}, fmt.Errorf("required embedded VCS facts are unavailable")
	}
	repository, err := domain.NewRepositoryIdentity(h.build.RepositoryIdentity())
	if err != nil {
		return cli.VersionData{}, err
	}
	commit, err := domain.NewBuildCommit(h.build.Revision())
	if err != nil {
		return cli.VersionData{}, err
	}
	return cli.NewVersionData(
		h.build.Product(),
		h.build.Executable(),
		h.build.Version(),
		repository,
		commit,
		h.build.GoVersion(),
		h.build.BuildTime(),
		h.build.TargetOS(),
		h.build.TargetArch(),
		h.defaultSource,
	)
}
