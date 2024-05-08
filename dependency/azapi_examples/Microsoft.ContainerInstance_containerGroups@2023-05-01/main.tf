terraform {
  required_providers {
    azapi = {
      source = "Azure/azapi"
    }
  }
}

provider "azapi" {
  skip_provider_registration = false
}

variable "resource_name" {
  type    = string
  default = "acctest0001"
}

variable "location" {
  type    = string
  default = "westeurope"
}

resource "azapi_resource" "resourceGroup" {
  type                      = "Microsoft.Resources/resourceGroups@2020-06-01"
  name                      = var.resource_name
  location                  = var.location
}

resource "azapi_resource" "containerGroup" {
  type      = "Microsoft.ContainerInstance/containerGroups@2023-05-01"
  parent_id = azapi_resource.resourceGroup.id
  name      = var.resource_name
  location  = var.location
  body = {
    properties = {
      containers = [
        {
          name = "hw"
          properties = {
            command = [
            ]
            environmentVariables = [
            ]
            image = "ubuntu:20.04"
            ports = [
              {
                port     = 80
                protocol = "TCP"
              },
            ]
            resources = {
              requests = {
                cpu        = 0.5
                memoryInGB = 0.5
              }
            }
          }
        },
      ]
      initContainers = [
      ]
      ipAddress = {
        autoGeneratedDomainNameLabelScope = "Unsecure"
        ports = [
          {
            port     = 80
            protocol = "TCP"
          },
        ]
        type = "Public"
      }
      osType        = "Linux"
      restartPolicy = "Always"
      volumes = [
      ]
    }
    tags = {
      environment = "Testing"
    }
    zones = [
    ]
  }
  schema_validation_enabled = false
  response_export_values    = ["*"]
}

