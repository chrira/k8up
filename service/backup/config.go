package backup

import (
	"github.com/spf13/viper"
	configPackage "github.com/vshn/k8up/config"
)

type config struct {
	configPackage.Global
	annotation              string
	defaultCheckSchedule    string
	podFilter               string
	backupCommandAnnotation string
	dataPath                string
	jobName                 string
	podName                 string
	restartPolicy           string
	podExecRoleName         string
	podExecAccountName      string
	fileExtensionAnnotation string
}

func newConfig() config {
	setDefaults()
	tmp := config{
		annotation:              viper.GetString("annotation"),
		defaultCheckSchedule:    viper.GetString("checkSchedule"),
		backupCommandAnnotation: viper.GetString("backupCommandAnnotation"),
		dataPath:                viper.GetString("dataPath"),
		jobName:                 viper.GetString("jobName"),
		podName:                 viper.GetString("podName"),
		podExecRoleName:         viper.GetString("PodExecRoleName"),
		podExecAccountName:      viper.GetString("PodExecAccountName"),
		fileExtensionAnnotation: viper.GetString("FileExtensionAnnotation"),
		Global:                  configPackage.New(),
	}
	return tmp
}

func setDefaults() {
	viper.SetDefault("annotation", "k8up.syn.tools/backup")
	viper.SetDefault("checkSchedule", "0 0 * * 0")
	viper.SetDefault("backupCommandAnnotation", "k8up.syn.tools/backupcommand")
	viper.SetDefault("dataPath", "/data")
	viper.SetDefault("jobName", "backupjob")
	viper.SetDefault("podName", "backupjob-pod")
	viper.SetDefault("PodExecRoleName", "pod-executor")
	viper.SetDefault("PodExecAccountName", "pod-executor")
	viper.SetDefault("FileExtensionAnnotation", "k8up.syn.tools/file-extension")
}
