package service

import (
	"context"
	"fmt"
)

// MigratePortalAppsToAccounts is disabled: the portal_apps_to_accounts step
// is not used and must not run. See .md/steps/portal_apps_to_accounts.md for
// why -- the old migration logic (portal.service_app -> im_account.app) is
// preserved in git history if the step is ever reinstated.
func (c *Converter) MigratePortalAppsToAccounts(_ context.Context) error {
	return fmt.Errorf("step %q is not used", StepPortalAppsToAccounts)
}
