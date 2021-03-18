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

package collections

import (
	"configcenter/src/common"
	"configcenter/src/storage/dal/types"
)

func init() {

	// 先注册未规范化的索引，如果索引出现冲突旧，删除未规范化的索引
	registerIndexes(common.BKTableNameServiceInstance, deprecatedServiceInstanceIndexes)
	registerIndexes(common.BKTableNameServiceInstance, commServiceInstanceIndexes)

}

//  新加和修改后的索引,索引名字一定要用对用的前缀，CCLogicUniqueIdxNamePrefix|common.CCLogicIndexNamePrefix

var commServiceInstanceIndexes = []types.Index{}

// deprecated 未规范化前的索引，只允许删除不允许新加和修改，
var deprecatedServiceInstanceIndexes = []types.Index{
	{
		Name: "idx_bkBizID",
		Keys: map[string]int32{
			"bk_biz_id": 1,
		},
		Background: true,
	},
	{
		Name: "idx_serviceTemplateID",
		Keys: map[string]int32{
			"service_template_id": 1,
		},
		Background: true,
	},
	{
		Name: "moduleID",
		Keys: map[string]int32{
			"bk_module_id": 1,
		},
		Background: true,
	},
	{
		Name: "bk_module_id_1_bk_biz_id_1",
		Keys: map[string]int32{
			"bk_module_id": 1,
			"bk_biz_id":    1,
		},
		Background: true,
	},
	{
		Name: "bk_biz_id_1_bk_host_id_1",
		Keys: map[string]int32{
			"bk_biz_id":  1,
			"bk_host_id": 1,
		},
		Background: true,
	},
	{
		Name: "idx_unique_id",
		Keys: map[string]int32{
			"id": 1,
		},
		Unique:     true,
		Background: true,
	},
	{
		Name: "bk_idx_host_id",
		Keys: map[string]int32{
			"bk_host_id": 1,
		},
		Background: true,
	},
}
