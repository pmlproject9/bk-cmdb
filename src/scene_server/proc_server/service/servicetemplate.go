/*
 * Tencent is pleased to support the open source community by making 蓝鲸 available.
 * Copyright (C) 2017-2018 THL A29 Limited, a Tencent company. All rights reserved.
 * Licensed under the MIT License (the "License"); you may not use this file except
 * in compliance with the License. You may obtain a copy of the License at
 * http://opensource.org/licenses/MIT
 * Unless required by applicable law or agreed to in writing, software distributed under
 * the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
 * either express or implied. See the License for the specific language governing permissions and
 * limitations under the License.
 */

package service

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"configcenter/src/ac/iam"
	"configcenter/src/common"
	"configcenter/src/common/auth"
	"configcenter/src/common/blog"
	"configcenter/src/common/errors"
	"configcenter/src/common/http/rest"
	"configcenter/src/common/mapstr"
	"configcenter/src/common/metadata"
	"configcenter/src/common/util"
)

func (ps *ProcServer) CreateServiceTemplate(ctx *rest.Contexts) {
	option := new(metadata.CreateServiceTemplateOption)
	if err := ctx.DecodeInto(option); err != nil {
		ctx.RespAutoError(err)
		return
	}

	newTemplate := &metadata.ServiceTemplate{
		BizID:             option.BizID,
		Name:              option.Name,
		ServiceCategoryID: option.ServiceCategoryID,
		SupplierAccount:   ctx.Kit.SupplierAccount,
	}

	var tpl *metadata.ServiceTemplate
	txnErr := ps.Engine.CoreAPI.CoreService().Txn().AutoRunTxn(ctx.Kit.Ctx, ctx.Kit.Header, func() error {
		var err error
		tpl, err = ps.CoreAPI.CoreService().Process().CreateServiceTemplate(ctx.Kit.Ctx, ctx.Kit.Header, newTemplate)
		if err != nil {
			blog.Errorf("create service template failed, err: %v", err)
			return err
		}

		// register service template resource creator action to iam
		if auth.EnableAuthorize() {
			iamInstance := metadata.IamInstanceWithCreator{
				Type:    string(iam.BizProcessServiceTemplate),
				ID:      strconv.FormatInt(tpl.ID, 10),
				Name:    tpl.Name,
				Creator: ctx.Kit.User,
			}
			_, err = ps.AuthManager.Authorizer.RegisterResourceCreatorAction(ctx.Kit.Ctx, ctx.Kit.Header, iamInstance)
			if err != nil {
				blog.Errorf("register created service template to iam failed, err: %v, rid: %s", err, ctx.Kit.Rid)
				return err
			}
		}

		return nil
	})

	if txnErr != nil {
		ctx.RespAutoError(txnErr)
		return
	}
	ctx.RespEntity(tpl)
}

func (ps *ProcServer) GetServiceTemplate(ctx *rest.Contexts) {
	templateIDStr := ctx.Request.PathParameter(common.BKServiceTemplateIDField)
	templateID, err := util.GetInt64ByInterface(templateIDStr)
	if err != nil {
		ctx.RespErrorCodeF(common.CCErrCommParamsInvalid, "create service template failed, err: %v", common.BKServiceTemplateIDField, err)
		return
	}
	template, err := ps.CoreAPI.CoreService().Process().GetServiceTemplate(ctx.Kit.Ctx, ctx.Kit.Header, templateID)
	if err != nil {
		ctx.RespWithError(err, common.CCErrCommHTTPDoRequestFailed, "get service template failed, err: %v", err)
		return
	}

	ctx.RespEntity(template)
}

// GetServiceTemplateDetail return more info than GetServiceTemplate
func (ps *ProcServer) GetServiceTemplateDetail(ctx *rest.Contexts) {
	templateIDStr := ctx.Request.PathParameter(common.BKServiceTemplateIDField)
	templateID, err := util.GetInt64ByInterface(templateIDStr)
	if err != nil {
		ctx.RespErrorCodeF(common.CCErrCommParamsInvalid, "create service template failed, err: %v", common.BKServiceTemplateIDField, err)
		return
	}
	templateDetail, err := ps.CoreAPI.CoreService().Process().GetServiceTemplateWithStatistics(ctx.Kit.Ctx, ctx.Kit.Header, templateID)
	if err != nil {
		ctx.RespWithError(err, common.CCErrCommHTTPDoRequestFailed, "get service template failed, err: %v", err)
		return
	}

	ctx.RespEntity(templateDetail)
}

func (ps *ProcServer) getHostIDByCondition(kit *rest.Kit, bizID int64, serviceTemplateIDs []int64,
	hostIDs []int64) ([]int64, errors.CCErrorCoder) {

	// 1、get module ids by template ids.
	moduleCond := mapstr.MapStr{
		common.BKAppIDField:             bizID,
		common.BKServiceTemplateIDField: mapstr.MapStr{common.BKDBIN: serviceTemplateIDs},
	}

	moduleFilter := &metadata.QueryCondition{
		Condition:      moduleCond,
		Fields:         []string{common.BKModuleIDField},
		DisableCounter: true,
	}

	moduleRes := new(metadata.ResponseModuleInstance)
	err := ps.CoreAPI.CoreService().Instance().ReadInstanceStruct(kit.Ctx, kit.Header, common.BKInnerObjIDModule,
		moduleFilter, moduleRes)
	if err != nil {
		blog.Errorf("get module failed, filter: %#v, err: %v, rid: %s", moduleFilter, err, kit.Rid)
		return nil, err
	}
	if err := moduleRes.CCError(); err != nil {
		blog.Errorf("get module failed, filter: %#v, err: %v, rid: %s", moduleFilter, err, kit.Rid)
		return nil, err
	}
	if len(moduleRes.Data.Info) == 0 {
		blog.Errorf("get module failed, filter: %#v, err: %v, rid: %s", moduleFilter, err, kit.Rid)
		return nil, kit.CCError.CCError(common.CCErrCommParamsInvalid)
	}
	modIDs := make([]int64, 0)

	for _, modID := range moduleRes.Data.Info {
		modIDs = append(modIDs, modID.ModuleID)
	}
	// 2、get the corresponding hostIDs list through the module ids.
	relationReq := &metadata.HostModuleRelationRequest{
		ApplicationID: bizID,
		ModuleIDArr:   modIDs,
		Page:          metadata.BasePage{Limit: common.BKNoLimit},
		Fields:        []string{common.BKModuleIDField, common.BKHostIDField},
	}

	// hostIDs are not empty in the invalid host scenario.
	if hostIDs != nil {
		relationReq.HostIDArr = hostIDs
	}
	hostRelations, e := ps.CoreAPI.CoreService().Host().GetHostModuleRelation(kit.Ctx, kit.Header, relationReq)
	if e != nil {
		blog.Errorf("get host module relation failed, err: %v, rid: %s", err, kit.Rid)
		return []int64{}, kit.CCError.CCError(common.CCErrCommHTTPDoRequestFailed)
	}

	hostModuleMap := make(map[int64]struct{})
	for _, item := range hostRelations.Info {
		hostModuleMap[item.HostID] = struct{}{}
	}
	result := make([]int64, 0)
	for hostID := range hostModuleMap {
		result = append(result, hostID)
	}
	return result, nil

}

// ExecServiceTemplateHostApplyRule execute the host automatic application task in the template scenario.
func (ps *ProcServer) ExecServiceTemplateHostApplyRule(ctx *rest.Contexts) {
	rid := ctx.Kit.Rid

	planReq := new(metadata.HostApplyServiceTemplateOption)
	if err := ctx.DecodeInto(planReq); err != nil {
		ctx.RespAutoError(err)
		return
	}
	hostIDs, err := ps.getHostIDByCondition(ctx.Kit, planReq.BizID, planReq.ServiceTemplateIDs, planReq.HostIDs)
	if err != nil {
		ctx.RespAutoError(err)
		return
	}
	txnErr := ps.Engine.CoreAPI.CoreService().Txn().AutoRunTxn(ctx.Kit.Ctx, ctx.Kit.Header, func() error {
		// enable host apply on service template
		updateOption := &metadata.UpdateOption{
			Condition: map[string]interface{}{
				common.BKFieldID:    map[string]interface{}{common.BKDBIN: planReq.ServiceTemplateIDs},
				common.BKAppIDField: planReq.BizID,
			},
			Data: map[string]interface{}{common.HostApplyEnabledField: true},
		}

		err := ps.CoreAPI.CoreService().Process().UpdateBatchServiceTemplate(ctx.Kit.Ctx, ctx.Kit.Header, updateOption)
		if err != nil {
			blog.Errorf("update service template failed, err: %v", err)
			return err
		}

		// save rules to database
		rulesOption := make([]metadata.CreateOrUpdateApplyRuleOption, 0)
		for _, rule := range planReq.AdditionalRules {
			rulesOption = append(rulesOption, metadata.CreateOrUpdateApplyRuleOption{
				AttributeID:       rule.AttributeID,
				ServiceTemplateID: rule.ServiceTemplateID,
				PropertyValue:     rule.PropertyValue,
			})
		}
		// 1、update or add rules.
		saveRuleOp := metadata.BatchCreateOrUpdateApplyRuleOption{Rules: rulesOption}
		if _, ccErr := ps.CoreAPI.CoreService().HostApplyRule().BatchUpdateHostApplyRule(ctx.Kit.Ctx, ctx.Kit.Header,
			planReq.BizID, saveRuleOp); ccErr != nil {
			blog.Errorf("update host rule failed, bizID: %s, req: %s, err: %v, rid: %s", planReq.BizID, saveRuleOp,
				ccErr, rid)
			return ccErr
		}

		// 2、delete rules.
		if len(planReq.RemoveRuleIDs) > 0 {
			removeOp := metadata.DeleteHostApplyRuleOption{
				RuleIDs:            planReq.RemoveRuleIDs,
				ServiceTemplateIDs: planReq.ServiceTemplateIDs,
			}
			if ccErr := ps.CoreAPI.CoreService().HostApplyRule().DeleteHostApplyRule(ctx.Kit.Ctx, ctx.Kit.Header,
				planReq.BizID, removeOp); ccErr != nil {
				blog.Errorf("delete apply rule failed, bizID: %d, req: %s, err: %v, rid: %s", planReq.BizID, removeOp,
					ccErr, rid)
				return ccErr
			}
		}

		return nil
	})

	if txnErr != nil {
		ctx.RespAutoError(&metadata.RespError{Msg: txnErr})
		return
	}

	// If the Changed flag is false or the request only contains the delete rule scenario, then there is no need to
	// update the host rule.
	if !planReq.Changed || len(planReq.AdditionalRules) == 0 {
		ctx.RespEntity(nil)
		return
	}

	// update host operation is not done in a transaction, since the successfully updated hosts need not roll back
	ctx.Kit.Header.Del(common.TransactionIdHeader)

	// host apply attribute rules to the host.
	err = ps.updateHostAttributes(ctx.Kit, planReq, hostIDs)
	if err != nil {
		ctx.RespAutoError(err)
		return
	}

	ctx.RespEntity(nil)
}

func (s *ProcServer) getUpdateDataStr(kit *rest.Kit, rules []metadata.CreateHostApplyRuleOption) (
	string, errors.CCErrorCoder) {
	attributeIDs := make([]int64, 0)
	attrIDmap := make(map[int64]struct{})
	for _, rule := range rules {
		if _, ok := attrIDmap[rule.AttributeID]; ok {
			continue
		}
		attrIDmap[rule.AttributeID] = struct{}{}
		attributeIDs = append(attributeIDs, rule.AttributeID)
	}

	attCond := &metadata.QueryCondition{
		Fields: []string{common.BKFieldID, common.BKPropertyIDField},
		Page:   metadata.BasePage{Limit: common.BKNoLimit},
		Condition: map[string]interface{}{
			common.BKFieldID: map[string]interface{}{
				common.BKDBIN: attributeIDs,
			},
		},
	}

	attrRes, err := s.CoreAPI.CoreService().Model().ReadModelAttr(kit.Ctx, kit.Header, common.BKInnerObjIDHost, attCond)
	if err != nil {
		blog.Errorf("read model attr failed, err: %v, attrCond: %#v, rid: %s", err, attCond, kit.Rid)
		return "", kit.CCError.CCError(common.CCErrCommHTTPDoRequestFailed)
	}

	attrMap := make(map[int64]string)
	for _, attr := range attrRes.Info {
		attrMap[attr.ID] = attr.PropertyID
	}

	fields := make([]string, len(rules))

	for index, field := range rules {
		value, _ := json.Marshal(field.PropertyValue)
		fields[index] = fmt.Sprintf(`"%s":%s`, attrMap[field.AttributeID], string(value))
	}

	sort.Strings(fields)
	return "{" + strings.Join(fields, ",") + "}", nil
}

func generateCondition(dataStr string, hostIDs []int64) (map[string]interface{}, map[string]interface{}) {
	data := make(map[string]interface{})
	_ = json.Unmarshal([]byte(dataStr), &data)

	cond := make([]map[string]interface{}, 0)

	for key, value := range data {
		cond = append(cond, map[string]interface{}{
			key: map[string]interface{}{common.BKDBNE: value},
		})
	}
	mergeCond := map[string]interface{}{
		common.BKHostIDField: map[string]interface{}{common.BKDBIN: hostIDs},
		common.BKDBOR:        cond,
	}
	return mergeCond, data
}

func (s *ProcServer) updateHostAttributes(kit *rest.Kit, planResult *metadata.HostApplyServiceTemplateOption,
	hostIDs []int64) errors.CCErrorCoder {

	dataStr, err := s.getUpdateDataStr(kit, planResult.AdditionalRules)
	if err != nil {
		return err
	}
	mergeCond, data := generateCondition(dataStr, hostIDs)
	counts, cErr := s.Engine.CoreAPI.CoreService().Count().GetCountByFilter(kit.Ctx, kit.Header,
		common.BKTableNameBaseHost, []map[string]interface{}{mergeCond})
	if cErr != nil {
		blog.Errorf("get hosts count failed, filter: %+v, err: %v, rid: %s", mergeCond, cErr, kit.Rid)
		return cErr
	}
	if counts[0] == 0 {
		blog.V(5).Infof("no hosts founded, filter: %+v, rid: %s", mergeCond, kit.Rid)
		return nil
	}

	// If there is no eligible host, then return directly.
	updateOp := &metadata.UpdateOption{Data: data, Condition: mergeCond}

	_, e := s.CoreAPI.CoreService().Instance().UpdateInstance(kit.Ctx, kit.Header, common.BKInnerObjIDHost, updateOp)
	if e != nil {
		blog.Errorf("update host failed, option: %s, err: %v, rid: %s", updateOp, e, kit.Rid)
		return errors.New(common.CCErrCommHTTPDoRequestFailed, e.Error())
	}
	return nil
}

// UpdateServiceTemplateHostApplyRule update host auto-apply rules in service template dimension.
func (ps *ProcServer) UpdateServiceTemplateHostApplyRule(ctx *rest.Contexts) {

	syncOpt := new(metadata.HostApplyServiceTemplateOption)
	if err := ctx.DecodeInto(syncOpt); err != nil {
		ctx.RespAutoError(err)
		return
	}

	if rawErr := syncOpt.Validate(); rawErr.ErrCode != 0 {
		ctx.RespAutoError(rawErr.ToCCError(ctx.Kit.CCError))
		return
	}

	taskInfo := metadata.APITaskDetail{}
	txnErr := ps.Engine.CoreAPI.CoreService().Txn().AutoRunTxn(ctx.Kit.Ctx, ctx.Kit.Header, func() error {
		taskRes, err := ps.CoreAPI.TaskServer().Task().Create(ctx.Kit.Ctx, ctx.Kit.Header,
			common.SyncServiceTemplateHostApplyTaskFlag, syncOpt.BizID, []interface{}{syncOpt})
		if err != nil {
			blog.Errorf("create service template host apply sync rule task failed, opt: %+v, err: %v, rid: %s",
				syncOpt, err, ctx.Kit.Rid)
			return err
		}
		taskInfo = taskRes
		blog.V(4).Infof("successfully create service template host apply sync task: %#v, rid: %s", taskRes, ctx.Kit.Rid)
		return nil
	})

	if txnErr != nil {
		ctx.RespAutoError(txnErr)
		return
	}

	ctx.RespEntity(metadata.HostApplyTaskResult{BizID: taskInfo.InstID, TaskID: taskInfo.TaskID})
}

// UpdateServiceTemplateHostApplyEnableStatus update object host if apply's status is enabled
func (ps *ProcServer) UpdateServiceTemplateHostApplyEnableStatus(ctx *rest.Contexts) {
	bizID, err := strconv.ParseInt(ctx.Request.PathParameter(common.BKAppIDField), 10, 64)
	if err != nil {
		blog.Errorf("parse bk_biz_id failed, err: %v, rid: %s", err, ctx.Kit.Rid)
		ctx.RespAutoError(ctx.Kit.CCError.CCErrorf(common.CCErrCommParamsNeedInt, common.BKAppIDField))
		return
	}

	requestBody := metadata.UpdateHostApplyEnableStatusOption{}
	if err := ctx.DecodeInto(&requestBody); err != nil {
		ctx.RespAutoError(err)
		return
	}

	if len(requestBody.IDs) == 0 {
		blog.Errorf("parse service template id failed, err: %v, rid: %s", err, ctx.Kit.Rid)
		ctx.RespAutoError(ctx.Kit.CCError.CCErrorf(common.CCErrCommParamsNeedInt, "service_template_ids"))
		return
	}
	updateOption := &metadata.UpdateOption{
		Condition: map[string]interface{}{
			common.BKAppIDField: bizID,
			common.BKFieldID:    mapstr.MapStr{common.BKDBIN: requestBody.IDs},
		},
		Data: map[string]interface{}{
			common.HostApplyEnabledField: requestBody.Enable,
		},
	}

	txnErr := ps.Engine.CoreAPI.CoreService().Txn().AutoRunTxn(ctx.Kit.Ctx, ctx.Kit.Header, func() error {
		err := ps.CoreAPI.CoreService().Process().UpdateBatchServiceTemplate(ctx.Kit.Ctx, ctx.Kit.Header, updateOption)
		if err != nil {
			blog.Errorf("update service template failed, err: %v", err)
			return err
		}

		// in the scenario of turning on the host's automatic application state, there is no clear rule action, and
		// return directly
		if requestBody.Enable {
			return nil
		}

		if requestBody.ClearRules {
			listRuleOption := metadata.ListHostApplyRuleOption{
				ServiceTemplateIDs: requestBody.IDs,
				Page: metadata.BasePage{
					Limit: common.BKNoLimit,
				},
			}
			listRuleResult, ccErr := ps.Engine.CoreAPI.CoreService().HostApplyRule().ListHostApplyRule(ctx.Kit.Ctx,
				ctx.Kit.Header, bizID, listRuleOption)
			if ccErr != nil {
				blog.Errorf("get list host apply rule failed, bizID: %d,listRuleOption: %#v, rid: %s", bizID,
					listRuleOption, ctx.Kit.Rid)
				return ccErr
			}
			ruleIDs := make([]int64, 0)
			for _, item := range listRuleResult.Info {
				ruleIDs = append(ruleIDs, item.ID)
			}
			if len(ruleIDs) > 0 {
				deleteRuleOption := metadata.DeleteHostApplyRuleOption{
					RuleIDs:            ruleIDs,
					ServiceTemplateIDs: requestBody.IDs,
				}
				if ccErr := ps.Engine.CoreAPI.CoreService().HostApplyRule().DeleteHostApplyRule(ctx.Kit.Ctx,
					ctx.Kit.Header, bizID, deleteRuleOption); ccErr != nil {
					blog.Errorf("delete list host apply rule failed, bizID: %d, listRuleOption: %#v, rid: %s",
						bizID, listRuleOption, ctx.Kit.Rid)
					return ccErr
				}
			}
		}
		return nil
	})
	if txnErr != nil {
		ctx.RespAutoError(txnErr)
		return
	}
	ctx.RespEntity(nil)
}
func (ps *ProcServer) DeleteHostApplyRule(ctx *rest.Contexts) {

	rid := ctx.Kit.Rid

	bizIDStr := ctx.Request.PathParameter(common.BKAppIDField)
	bizID, err := strconv.ParseInt(bizIDStr, 10, 64)
	if err != nil {
		blog.Errorf("DeleteHostApplyRule failed, parse biz id failed, bizIDStr: %s, err: %v,rid:%s", bizIDStr, err, rid)
		ctx.RespAutoError(ctx.Kit.CCError.Errorf(common.CCErrCommParamsInvalid, common.BKAppIDField))
		return
	}
	option := metadata.DeleteHostApplyRuleOption{}
	if err := ctx.DecodeInto(&option); nil != err {
		ctx.RespAutoError(err)
		return
	}

	if rawErr := option.Validate(); rawErr.ErrCode != 0 {
		ctx.RespAutoError(rawErr.ToCCError(ctx.Kit.CCError))
		return
	}

	txnErr := ps.Engine.CoreAPI.CoreService().Txn().AutoRunTxn(ctx.Kit.Ctx, ctx.Kit.Header, func() error {
		if err := ps.CoreAPI.CoreService().HostApplyRule().DeleteHostApplyRule(ctx.Kit.Ctx, ctx.Kit.Header, bizID, option); err != nil {
			blog.ErrorJSON("DeleteHostApplyRule failed, core service DeleteHostApplyRule failed, bizID: %s, option: %s, err: %s, rid: %s", bizID, option, err.Error(), rid)
			return err
		}
		return nil
	})

	if txnErr != nil {
		ctx.RespAutoError(txnErr)
		return
	}
	ctx.RespEntity(make(map[string]interface{}))

}
func (ps *ProcServer) UpdateServiceTemplate(ctx *rest.Contexts) {
	option := new(metadata.UpdateServiceTemplateOption)
	if err := ctx.DecodeInto(option); err != nil {
		ctx.RespAutoError(err)
		return
	}

	updateParam := &metadata.ServiceTemplate{
		ID:                option.ID,
		Name:              option.Name,
		ServiceCategoryID: option.ServiceCategoryID,
	}

	var tpl *metadata.ServiceTemplate
	txnErr := ps.Engine.CoreAPI.CoreService().Txn().AutoRunTxn(ctx.Kit.Ctx, ctx.Kit.Header, func() error {
		var err error
		tpl, err = ps.CoreAPI.CoreService().Process().UpdateServiceTemplate(ctx.Kit.Ctx, ctx.Kit.Header, option.ID, updateParam)
		if err != nil {
			blog.Errorf("update service template failed, err: %v", err)
			return err
		}
		return nil
	})

	if txnErr != nil {
		ctx.RespAutoError(txnErr)
		return
	}
	ctx.RespEntity(tpl)
}

func (ps *ProcServer) ListServiceTemplates(ctx *rest.Contexts) {
	input := new(metadata.ListServiceTemplateInput)
	if err := ctx.DecodeInto(input); err != nil {
		ctx.RespAutoError(err)
		return
	}

	if input.Page.Limit > common.BKMaxPageSize {
		ctx.RespErrorCodeOnly(common.CCErrCommPageLimitIsExceeded, "list service template, but page limit:%d is over limited.", input.Page.Limit)
		return
	}

	option := metadata.ListServiceTemplateOption{
		BusinessID:        input.BizID,
		Page:              input.Page,
		ServiceCategoryID: &input.ServiceCategoryID,
		Search:            input.Search,
		IsExact:           input.IsExact,
	}
	temp, err := ps.CoreAPI.CoreService().Process().ListServiceTemplates(ctx.Kit.Ctx, ctx.Kit.Header, &option)
	if err != nil {
		ctx.RespWithError(err, common.CCErrCommHTTPDoRequestFailed, "list service template failed, input: %+v", input)
		return
	}

	ctx.RespEntity(temp)
}

// FindServiceTemplateCountInfo find count info of service templates
func (ps *ProcServer) FindServiceTemplateCountInfo(ctx *rest.Contexts) {
	bizID, err := strconv.ParseInt(ctx.Request.PathParameter(common.BKAppIDField), 10, 64)
	if err != nil {
		blog.Errorf("FindServiceTemplateCountInfo failed, parse bk_biz_id error, err: %s, rid: %s", err, ctx.Kit.Rid)
		ctx.RespAutoError(ctx.Kit.CCError.CCErrorf(common.CCErrCommParamsIsInvalid, "bk_biz_id"))
		return
	}

	input := new(metadata.FindServiceTemplateCountInfoOption)
	if err := ctx.DecodeInto(input); nil != err {
		ctx.RespAutoError(err)
		return
	}

	rawErr := input.Validate()
	if rawErr.ErrCode != 0 {
		ctx.RespAutoError(rawErr.ToCCError(ctx.Kit.CCError))
		return
	}

	// generate count conditions
	filters := make([]map[string]interface{}, len(input.ServiceTemplateIDs))
	for idx, serviceTemplateID := range input.ServiceTemplateIDs {
		filters[idx] = map[string]interface{}{
			common.BKAppIDField:             bizID,
			common.BKServiceTemplateIDField: serviceTemplateID,
		}
	}

	// process templates reference count
	processTemplateCounts, err := ps.CoreAPI.CoreService().Count().GetCountByFilter(ctx.Kit.Ctx, ctx.Kit.Header, common.BKTableNameProcessTemplate, filters)
	if err != nil {
		ctx.RespWithError(err, common.CCErrProcGetProcessTemplatesFailed, "count process template by filters: %+v failed.", filters)
		return
	}
	if len(processTemplateCounts) != len(input.ServiceTemplateIDs) {
		ctx.RespWithError(ctx.Kit.CCError.CCError(common.CCErrProcGetProcessTemplatesFailed), common.CCErrProcGetProcessTemplatesFailed,
			"the count of process must be equal with the count of service templates, filters:%#v", filters)
		return
	}

	// module reference count
	moduleCounts, err := ps.CoreAPI.CoreService().Count().GetCountByFilter(ctx.Kit.Ctx, ctx.Kit.Header, common.BKTableNameBaseModule, filters)
	if err != nil {
		ctx.RespWithError(err, common.CCErrTopoModuleSelectFailed, "count process template by filters: %+v failed.", filters)
		return
	}
	if len(moduleCounts) != len(input.ServiceTemplateIDs) {
		ctx.RespWithError(ctx.Kit.CCError.CCError(common.CCErrTopoModuleSelectFailed), common.CCErrTopoModuleSelectFailed,
			"the count of modules must be equal with the count of service templates, filters:%#v", filters)
		return
	}

	// service instance reference count
	serviceInstanceCounts, err := ps.CoreAPI.CoreService().Count().GetCountByFilter(ctx.Kit.Ctx, ctx.Kit.Header, common.BKTableNameServiceInstance, filters)
	if err != nil {
		ctx.RespWithError(err, common.CCErrProcGetServiceInstancesFailed, "count process template by filters: %+v failed.", filters)
		return
	}
	if len(serviceInstanceCounts) != len(input.ServiceTemplateIDs) {
		ctx.RespWithError(ctx.Kit.CCError.CCError(common.CCErrProcGetServiceInstancesFailed), common.CCErrProcGetServiceInstancesFailed,
			"the count of service instance must be equal with the count of service templates, filters:%#v", filters)
		return
	}

	result := make([]metadata.FindServiceTemplateCountInfoResult, 0)
	for idx, serviceTemplateID := range input.ServiceTemplateIDs {
		result = append(result, metadata.FindServiceTemplateCountInfoResult{
			ServiceTemplateID:    serviceTemplateID,
			ProcessTemplateCount: processTemplateCounts[idx],
			ServiceInstanceCount: serviceInstanceCounts[idx],
			ModuleCount:          moduleCounts[idx],
		})
	}

	ctx.RespEntity(result)
}

// a service template can be delete only when it is not be used any more,
// which means that no process instance belongs to it.
func (ps *ProcServer) DeleteServiceTemplate(ctx *rest.Contexts) {
	input := new(metadata.DeleteServiceTemplatesInput)
	if err := ctx.DecodeInto(input); err != nil {
		ctx.RespAutoError(err)
		return
	}

	txnErr := ps.Engine.CoreAPI.CoreService().Txn().AutoRunTxn(ctx.Kit.Ctx, ctx.Kit.Header, func() error {
		err := ps.CoreAPI.CoreService().Process().DeleteServiceTemplate(ctx.Kit.Ctx, ctx.Kit.Header, input.ServiceTemplateID)
		if err != nil {
			blog.Errorf("delete service template: %d failed", input.ServiceTemplateID)
			return ctx.Kit.CCError.CCError(common.CCErrProcDeleteServiceTemplateFailed)
		}
		return nil
	})

	if txnErr != nil {
		ctx.RespAutoError(txnErr)
		return
	}
	ctx.RespEntity(nil)
}

// GetServiceTemplateSyncStatus check if service templates or modules with template need sync, return the status
func (ps *ProcServer) GetServiceTemplateSyncStatus(ctx *rest.Contexts) {
	bizIDStr := ctx.Request.PathParameter(common.BKAppIDField)
	bizID, err := strconv.ParseInt(bizIDStr, 10, 64)
	if err != nil || bizID <= 0 {
		ctx.RespAutoError(ctx.Kit.CCError.CCErrorf(common.CCErrCommParamsIsInvalid, common.BKAppIDField))
		return
	}

	opt := new(metadata.GetServiceTemplateSyncStatusOption)
	if err := ctx.DecodeInto(opt); err != nil {
		ctx.RespAutoError(err)
		return
	}

	const maxIDLen = 100
	if opt.IsPartial {
		if len(opt.ServiceTemplateIDs) == 0 {
			ctx.RespAutoError(ctx.Kit.CCError.CCErrorf(common.CCErrCommParamsNeedSet, "service_template_ids"))
			return
		}

		if len(opt.ServiceTemplateIDs) > maxIDLen {
			ctx.RespAutoError(ctx.Kit.CCError.CCErrorf(common.CCErrCommXXExceedLimit, "service_template_ids", maxIDLen))
			return
		}

		moduleCond := map[string]interface{}{
			common.BKAppIDField: bizID,
			common.BKServiceTemplateIDField: map[string]interface{}{
				common.BKDBIN: opt.ServiceTemplateIDs,
			},
		}

		statuses, _, err := ps.Logic.GetSvcTempSyncStatus(ctx.Kit, bizID, moduleCond, true)
		if err != nil {
			ctx.RespAutoError(err)
			return
		}

		ctx.RespEntity(metadata.ServiceTemplateSyncStatus{ServiceTemplates: statuses})
		return
	} else {
		if len(opt.ModuleIDs) == 0 {
			ctx.RespAutoError(ctx.Kit.CCError.CCErrorf(common.CCErrCommParamsNeedSet, "bk_module_ids"))
			return
		}

		if len(opt.ModuleIDs) > maxIDLen {
			ctx.RespAutoError(ctx.Kit.CCError.CCErrorf(common.CCErrCommXXExceedLimit, "bk_module_ids", maxIDLen))
			return
		}

		moduleCond := map[string]interface{}{
			common.BKModuleIDField: map[string]interface{}{
				common.BKDBIN: opt.ModuleIDs,
			},
			common.BKAppIDField: bizID,
			common.BKServiceTemplateIDField: map[string]interface{}{
				common.BKDBNE: common.ServiceTemplateIDNotSet,
			},
		}

		_, statuses, err := ps.Logic.GetSvcTempSyncStatus(ctx.Kit, bizID, moduleCond, false)
		if err != nil {
			ctx.RespAutoError(err)
			return
		}

		ctx.RespEntity(metadata.ServiceTemplateSyncStatus{Modules: statuses})
		return
	}
}
