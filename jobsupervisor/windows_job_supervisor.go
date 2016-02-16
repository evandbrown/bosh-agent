package jobsupervisor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"text/template"

	boshdirs "github.com/cloudfoundry/bosh-agent/settings/directories"
	bosherr "github.com/cloudfoundry/bosh-utils/errors"
	boshlog "github.com/cloudfoundry/bosh-utils/logger"
	boshsys "github.com/cloudfoundry/bosh-utils/system"
)

const (
	serviceDescription = "vcap"

	serviceWrapperExeFileName    = "job-service-wrapper.exe"
	serviceWrapperConfigFileName = "job-service-wrapper.xml"

	addJobScript = `
New-Service -Name "%s" -Description "` + serviceDescription + `" -binaryPathName "%s" -StartupType Automatic
`
	startJobScript = `
(get-wmiobject win32_service -filter "description='` + serviceDescription + `'") | ForEach{ Start-Service $_.Name }
`
	stopJobScript = `
(get-wmiobject win32_service -filter "description='` + serviceDescription + `'") | ForEach{ Stop-Service $_.Name }
`
	deleteAllJobsScript = `
(get-wmiobject win32_service -filter "description='` + serviceDescription + `'") | ForEach{ $_.delete() }
`
	getStatusScript = `
(get-wmiobject win32_service -filter "description='` + serviceDescription + `'") | ForEach{ $_.State }
`
	serviceWrapperTemplate = `
<service>
  <id>{{ .ID }}</id>
  <name>{{ .Name }}</name>
  <description>` + serviceDescription + `</description>
  <executable>{{ .Executable }}</executable>
  <arguments>{{ .Arguments }}</arguments>
  <log mode="append"/>
  <onfailure action="restart" delay="5 sec"/>
</service>
`
)

type WindowsServiceWrapperConfig struct {
	ID         string
	Name       string
	Executable string
	Arguments  string
}

type WindowsProcessConfig struct {
	Processes []WindowsProcess `json:"processes"`
}

type WindowsProcess struct {
	Name       string   `json:"name"`
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

type windowsJobSupervisor struct {
	cmdRunner   boshsys.CmdRunner
	dirProvider boshdirs.Provider
	fs          boshsys.FileSystem
	logger      boshlog.Logger
	logTag      string
}

func NewWindowsJobSupervisor(
	cmdRunner boshsys.CmdRunner,
	dirProvider boshdirs.Provider,
	fs boshsys.FileSystem,
	logger boshlog.Logger,
) JobSupervisor {
	return &windowsJobSupervisor{
		cmdRunner:   cmdRunner,
		dirProvider: dirProvider,
		fs:          fs,
		logger:      logger,
		logTag:      "windowsJobSupervisor",
	}
}

func (s *windowsJobSupervisor) Reload() error {
	return nil
}

func (s *windowsJobSupervisor) Start() error {
	_, _, _, err := s.cmdRunner.RunCommand("powershell", "-noprofile", "-noninteractive", "/C", startJobScript)
	if err != nil {
		return bosherr.WrapError(err, "Starting windows job process")
	}

	err = s.fs.RemoveAll(s.stoppedFilePath())
	if err != nil {
		return bosherr.WrapError(err, "Removing stopped file")
	}

	return nil
}

func (s *windowsJobSupervisor) Stop() error {
	_, _, _, err := s.cmdRunner.RunCommand("powershell", "-noprofile", "-noninteractive", "/C", stopJobScript)
	if err != nil {
		return bosherr.WrapError(err, "Stopping services")
	}

	err = s.fs.WriteFileString(s.stoppedFilePath(), "")
	if err != nil {
		return bosherr.WrapError(err, "Creating stopped file")
	}

	return nil
}

func (s *windowsJobSupervisor) Unmonitor() error {
	return nil
}

func (s *windowsJobSupervisor) Status() (status string) {
	if s.fs.FileExists(s.stoppedFilePath()) {
		return "stopped"
	}

	stdout, _, _, err := s.cmdRunner.RunCommand("powershell", "-noprofile", "-noninteractive", "/C", getStatusScript)
	if err != nil {
		return "failing"
	}

	statuses := strings.Split(strings.TrimSpace(stdout), "\r\n")
	s.logger.Debug(s.logTag, "Got statuses %#v", statuses)

	for _, status := range statuses {
		if status != "Running" {
			return "failing"
		}
	}

	return "running"
}

func (s *windowsJobSupervisor) Processes() ([]Process, error) {
	return []Process{}, nil
}

func (s *windowsJobSupervisor) AddJob(jobName string, jobIndex int, configPath string) error {
	configFileContents, err := s.fs.ReadFile(configPath)
	if err != nil {
		return err
	}

	var processConfig WindowsProcessConfig
	err = json.Unmarshal(configFileContents, &processConfig)
	if err != nil {
		return err
	}

	for _, process := range processConfig.Processes {
		serviceConfig := WindowsServiceWrapperConfig{
			ID:         jobName,
			Name:       process.Name,
			Executable: process.Executable,
			Arguments:  strings.Join(process.Args, " "),
		}

		buffer := bytes.NewBuffer([]byte{})
		t := template.Must(template.New("service-wrapper-config").Parse(serviceWrapperTemplate))
		err := t.Execute(buffer, serviceConfig)
		if err != nil {
			return err
		}

		s.logger.Debug(s.logTag, "Configuring service wrapper for job '%s' with configPath '%s'", jobName, configPath)

		jobDir := filepath.Dir(configPath)
		serviceWrapperConfigFile := filepath.Join(jobDir, serviceWrapperConfigFileName)
		err = s.fs.WriteFile(serviceWrapperConfigFile, buffer.Bytes())
		if err != nil {
			return err
		}

		serviceWrapperExePath := filepath.Join(s.dirProvider.BoshBinDir(), serviceWrapperExeFileName)
		err = s.fs.CopyFile(serviceWrapperExePath, filepath.Join(jobDir, serviceWrapperExeFileName))
		if err != nil {
			return bosherr.WrapErrorf(err, "Copying service wrapper in job directory '%s'", jobDir)
		}

		cmdToRun := filepath.Join(jobDir, serviceWrapperExeFileName)

		psScript := fmt.Sprintf(addJobScript, jobName, cmdToRun)
		_, _, _, err = s.cmdRunner.RunCommand("powershell", "-noprofile", "-noninteractive", "/C", psScript)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *windowsJobSupervisor) RemoveAllJobs() error {
	_, _, _, err := s.cmdRunner.RunCommand("powershell", "-noprofile", "-noninteractive", "/C", deleteAllJobsScript)
	return err
}

func (s *windowsJobSupervisor) MonitorJobFailures(handler JobFailureHandler) error {
	return nil
}

func (s *windowsJobSupervisor) stoppedFilePath() string {
	return filepath.Join(s.dirProvider.MonitDir(), "stopped")
}