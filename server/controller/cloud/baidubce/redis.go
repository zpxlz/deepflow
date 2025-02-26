/*
 * Copyright (c) 2024 Yunshan Networks
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package baidubce

import (
	"time"

	"github.com/baidubce/bce-sdk-go/services/scs"
	"github.com/deepflowio/deepflow/server/controller/cloud/model"
	"github.com/deepflowio/deepflow/server/controller/common"
	"github.com/deepflowio/deepflow/server/libs/logger"
)

func (b *BaiduBce) getRedisInstances(vpcIdToLcuuid, networkIdToLcuuid, zoneNameToAZLcuuid map[string]string) ([]model.RedisInstance, []model.VInterface, []model.IP, error) {
	var redisInstances []model.RedisInstance
	var vinterfaces []model.VInterface
	var ips []model.IP

	log.Debug("get redis starting", logger.NewORGPrefix(b.orgID))

	scsClient, _ := scs.NewClient(b.secretID, b.secretKey, "redis."+b.endpoint)
	scsClient.Config.ConnectionTimeoutInMillis = b.httpTimeout * 1000
	marker := ""
	args := &scs.ListInstancesArgs{}
	results := make([]*scs.ListInstancesResult, 0)
	for {
		args.Marker = marker
		startTime := time.Now()
		result, err := scsClient.ListInstances(args)
		if err != nil {
			log.Error(err, logger.NewORGPrefix(b.orgID))
			return nil, nil, nil, err
		}
		b.cloudStatsd.RefreshAPIMoniter("ListRedisInstance", len(result.Instances), startTime)
		results = append(results, result)
		if !result.IsTruncated {
			break
		}
		marker = result.NextMarker
	}

	b.debugger.WriteJson("ListRedisInstance", " ", structToJson(results))
	for _, result := range results {
		for _, ret := range result.Instances {
			redis, err := scsClient.GetInstanceDetail(ret.InstanceID)
			if err != nil {
				log.Errorf("get instance detail error (%s)", err.Error(), logger.NewORGPrefix(b.orgID))
				continue
			}

			if redis.InstanceStatus != "Running" {
				log.Infof("redis (%s) invalid status (%s)", redis.InstanceName, redis.InstanceStatus, logger.NewORGPrefix(b.orgID))
				continue
			}
			vpcLcuuid, ok := vpcIdToLcuuid[redis.VpcID]
			if !ok {
				log.Infof("redis (%s) vpc (%s) not found", redis.InstanceName, redis.VpcID, logger.NewORGPrefix(b.orgID))
				continue
			}
			if len(redis.ZoneNames) == 0 {
				log.Infof("redis (%s) zone not found", redis.InstanceName, logger.NewORGPrefix(b.orgID))
				continue
			}
			azLcuuid, ok := zoneNameToAZLcuuid[redis.ZoneNames[0]]
			if !ok {
				log.Infof("redis (%s) zone (%s) not found", redis.InstanceID, redis.ZoneNames[0], logger.NewORGPrefix(b.orgID))
				continue
			}
			redisLcuuid := common.GenerateUUIDByOrgID(b.orgID, redis.InstanceID)
			redisInstances = append(redisInstances, model.RedisInstance{
				Lcuuid:       redisLcuuid,
				Name:         redis.InstanceName,
				Label:        redis.InstanceID,
				VPCLcuuid:    vpcLcuuid,
				AZLcuuid:     azLcuuid,
				RegionLcuuid: b.regionLcuuid,
				InternalHost: redis.VnetIP,
				PublicHost:   redis.Eip,
				State:        common.REDIS_STATE_RUNNING,
				Version:      "Redis " + redis.EngineVersion,
			})
			b.azLcuuidToResourceNum[azLcuuid]++

			if len(redis.Subnets) == 0 {
				log.Infof("redis (%s) without subnets", redis.InstanceName, logger.NewORGPrefix(b.orgID))
				continue
			}
			networkLcuuid, ok := networkIdToLcuuid[redis.Subnets[0].SubnetID]
			if !ok {
				log.Infof("redis (%s) network (%s) not found", redis.InstanceName, redis.Subnets[0].SubnetID, logger.NewORGPrefix(b.orgID))
				continue
			}

			for index, ip := range []string{redis.VnetIP, redis.Eip} {
				if ip == "" {
					continue
				}

				var ipType int
				switch index {
				case 0:
					ipType = common.VIF_TYPE_LAN
				case 1:
					ipType = common.VIF_TYPE_WAN
				}

				vinterfaceLcuuid := common.GenerateUUIDByOrgID(b.orgID, redisLcuuid+ip)
				vinterfaces = append(vinterfaces, model.VInterface{
					Lcuuid:        vinterfaceLcuuid,
					Type:          ipType,
					Mac:           common.VIF_DEFAULT_MAC,
					DeviceLcuuid:  redisLcuuid,
					DeviceType:    common.VIF_DEVICE_TYPE_REDIS_INSTANCE,
					NetworkLcuuid: networkLcuuid,
					VPCLcuuid:     vpcLcuuid,
					RegionLcuuid:  b.regionLcuuid,
				})
				ips = append(ips, model.IP{
					Lcuuid:           common.GenerateUUIDByOrgID(b.orgID, vinterfaceLcuuid+ip),
					VInterfaceLcuuid: vinterfaceLcuuid,
					IP:               ip,
					SubnetLcuuid:     common.GenerateUUIDByOrgID(b.orgID, networkLcuuid),
					RegionLcuuid:     b.regionLcuuid,
				})
			}
		}
	}
	log.Debug("get redis complete", logger.NewORGPrefix(b.orgID))
	return redisInstances, vinterfaces, ips, nil
}
