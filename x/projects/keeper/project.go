package keeper

import (
	"fmt"
	"strconv"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/lavanet/lava/utils"
	"github.com/lavanet/lava/x/projects/types"
)

func (k Keeper) GetProjectForBlock(ctx sdk.Context, projectID string, blockHeight uint64) (types.Project, error) {
	var project types.Project

	err, found := k.projectsFS.FindEntry(ctx, projectID, blockHeight, &project)
	if err != nil || !found {
		return project, utils.LavaError(ctx, ctx.Logger(), "GetProjectForBlock_not_found", map[string]string{"project": projectID, "blockHeight": strconv.FormatUint(blockHeight, 10)}, "project not found")
	}

	return project, nil
}

func (k Keeper) GetProjectDeveloperData(ctx sdk.Context, developerKey string, blockHeight uint64) (types.ProtoDeveloperData, error) {
	var projectDeveloperData types.ProtoDeveloperData
	err, found := k.developerKeysFS.FindEntry(ctx, developerKey, blockHeight, &projectDeveloperData)
	if err != nil || !found {
		return types.ProtoDeveloperData{}, fmt.Errorf("GetProjectIDForDeveloper_invalid_key, the requesting key is not registered to a project, developer: %s", developerKey)
	}
	return projectDeveloperData, nil
}

func (k Keeper) GetProjectForDeveloper(ctx sdk.Context, developerKey string, blockHeight uint64) (proj types.Project, vrfpk string, errRet error) {
	var project types.Project
	projectDeveloperData, err := k.GetProjectDeveloperData(ctx, developerKey, blockHeight)
	if err != nil {
		return project, "", err
	}

	err, found := k.projectsFS.FindEntry(ctx, projectDeveloperData.ProjectID, blockHeight, &project)
	if err != nil {
		return project, "", err
	}

	if !found {
		return project, "", utils.LavaError(ctx, ctx.Logger(), "GetProjectForDeveloper_project_not_found", map[string]string{"developer": developerKey, "project": projectDeveloperData.ProjectID}, "the developers project was not found")
	}

	return project, projectDeveloperData.Vrfpk, nil
}

func (k Keeper) AddKeysToProject(ctx sdk.Context, projectID string, adminKey string, projectKeys []types.ProjectKey) error {
	var project types.Project
	err, found := k.projectsFS.FindEntry(ctx, projectID, uint64(ctx.BlockHeight()), &project)
	if err != nil || !found {
		return utils.LavaError(ctx, ctx.Logger(), "AddProjectKeys_project_not_found", map[string]string{"project": projectID}, "project id not found")
	}

	// check if the admin key is valid
	if !project.IsAdminKey(adminKey) {
		return utils.LavaError(ctx, ctx.Logger(), "AddProjectKeys_not_admin", map[string]string{"project": projectID}, "the requesting key is not admin key")
	}

	// check that those keys are unique for developers
	for _, projectKey := range projectKeys {
		err = k.RegisterDeveloperKey(ctx, projectKey.Key, project.Index, uint64(ctx.BlockHeight()), projectKey.Vrfpk)
		if err != nil {
			return err
		}

		project.AppendKey(projectKey)
	}

	return k.projectsFS.AppendEntry(ctx, projectID, uint64(ctx.BlockHeight()), &project)
}

func (k Keeper) GetProjectDevelopersPolicy(ctx sdk.Context, developerKey string, blockHeight uint64, adminPolicy bool) (policy types.Policy, err error) {
	project, _, err := k.GetProjectForDeveloper(ctx, developerKey, blockHeight)
	if err != nil {
		return types.Policy{}, err
	}

	if adminPolicy {
		return project.AdminPolicy, nil
	}
	return project.SubscriptionPolicy, nil
}

func (k Keeper) AddComputeUnitsToProject(ctx sdk.Context, project *types.Project, cu uint64) (err error) {
	if project == nil {
		return utils.LavaError(ctx, k.Logger(ctx), "AddComputeUnitsToProject_project_nil", nil, "project is nil")
	}
	project.UsedCu += cu
	return k.projectsFS.ModifyEntry(ctx, project.Index, uint64(ctx.BlockHeight()), project)
}

func (k Keeper) ValidateChainPolicies(ctx sdk.Context, policy types.Policy) error {
	// validate chainPolicies
	for _, chainPolicy := range policy.GetChainPolicies() {
		// get spec and make sure it's enabled
		spec, found := k.specKeeper.GetSpec(ctx, chainPolicy.GetChainId())
		if !found {
			return utils.LavaError(ctx, k.Logger(ctx), "validateChainPolicies_spec_not_found", map[string]string{"specIndex": spec.GetIndex()}, "policy's spec not found")
		}
		if !spec.GetEnabled() {
			return utils.LavaError(ctx, k.Logger(ctx), "validateChainPolicies_spec_not_enabled", map[string]string{"specIndex": spec.GetIndex()}, "policy's spec not enabled")
		}

		// go over the chain policy's APIs and make sure that they are part of the spec
		for _, policyApi := range chainPolicy.GetApis() {
			foundApi := false
			for _, api := range spec.GetApis() {
				if api.GetName() == policyApi {
					foundApi = true
				}
			}
			if !foundApi {
				details := map[string]string{
					"specIndex": spec.GetIndex(),
					"API":       policyApi,
				}
				return utils.LavaError(ctx, k.Logger(ctx), "validateChainPolicies_chain_policy_api_not_found", details, "policy's spec's API not found")
			}
		}
	}

	return nil
}
