# MySQL-operator

## Introduction

MySQL-Operator is a kubernetes operator with the goal to create and manage a MySQL cluster within Kubernetes. This operator will set up a StatefulSet consisting of 3 database nodes which will be group replicated to achieve High Availability (HA). 
To highlight what will be created after a Custom Resource(CR) gets applied the following overview can be used:
![Overview of the operator highlighting the StatefulSet being placed over 3 worker nodes. There is group replication applied between the mysql nodes](overview.png)

As showcased above the operator will apply a StatefulSet consisting of 3 relpica's each using a Percona MySQL image. 
Next to that there will be group replication applied between each MySQL node, this to achieve high availability on the cluster.

The operator supports passing along a custom `my.cnf` configuration file in the custom recource. The custom resources can be found under the [samples](/config/samples/) directory.

## Prerequisites
Before installing this project on a new Kubernetes cluster make sure [cert-manager](https://cert-manager.io/docs/installation/) is present on the cluster.
(Optional): Have [ProxySQL](https://proxysql.com/) ready in a deployment. For now this has to be [setup manually](https://proxysql.com/documentation/proxysql-configuration/). 

## Installation

Please Visit [Installation](https://github.com/elninotech/mysql-operator/wiki/Installation) to find the instructions how to deploy the operator.

## Operator SDK

This project uses [Operator SDK](https://sdk.operatorframework.io/) for its scaffolding and API generation. The operator runs as a controller in Kubernetes. To edit its behavior the file under [mysql_controller.go](/internal/controller/mysql_controller.go) contains the main logic of the operator. To manage fields in the Custom Resource Definition(CRD) & Edit the Custom Resource(CR) options the API has to be edited. This can be done under [mysql_types.go](/api/v1alpha1/mysql_types.go). The types are defined as such:

For the type MysqlConf a key-value mapping is created which enables single line config changes in the CR.
GroupReplicationSpec are group replication specific API options.


## Future Todos
- Integrate ProxySQL Rollout into cluster
- Implement Backup & Restore
- Implement User & Database creation
