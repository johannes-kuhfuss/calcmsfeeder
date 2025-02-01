package service

import (
	"github.com/johannes-kuhfuss/calcmsfeeder/config"
)

var (
	cfgCal        config.AppConfig
	calCmsService DefaultCalCmsService
)

const ()

func setupTestCal() func() {
	config.InitConfig(config.EnvFile, &cfgCal)
	calCmsService = NewCalCmsService(&cfgCal)
	return func() {
	}
}
