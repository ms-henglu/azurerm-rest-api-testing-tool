package coverage

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/go-openapi/loads"
	openapiSpec "github.com/go-openapi/spec"
	"github.com/hashicorp/golang-lru/v2"
)

// http://azure.github.io/autorest/extensions/#x-ms-discriminator-value
const msExtensionDiscriminator = "x-ms-discriminator-value"

var (
	// {swaggerPath: doc Object}
	swaggerCache, _ = lru.New[string, *loads.Document](20)

	// {swaggerPath: {parentModelName: {childModelName: nil}}}
	allOfTableCache, _ = lru.New[string, map[string]map[string]interface{}](10)
)

func loadSwagger(swaggerPath string) (*loads.Document, error) {
	if doc, ok := swaggerCache.Get(swaggerPath); ok {
		return doc, nil
	}

	doc, err := loads.JSONSpec(swaggerPath)
	if err != nil {
		return nil, err
	}
	swaggerCache.Add(swaggerPath, doc)
	return doc, nil
}

func getAllOfTable(swaggerPath string) (map[string]map[string]interface{}, error) {
	if vt, ok := allOfTableCache.Get(swaggerPath); ok {
		return vt, nil
	}

	doc, err := loadSwagger(swaggerPath)
	if err != nil {
		return nil, err
	}
	spec := doc.Spec()

	allOfTable := map[string]map[string]interface{}{}
	for k, v := range spec.Definitions {
		if len(v.AllOf) > 0 {
			for _, allOf := range v.AllOf {
				if allOf.Ref.String() != "" {
					modelName, absPath := SchemaNamePathFromRef(swaggerPath, allOf.Ref)
					if absPath != swaggerPath {
						continue
					}

					if _, ok := allOfTable[modelName]; !ok {
						allOfTable[modelName] = map[string]interface{}{}
					}
					allOfTable[modelName][k] = nil
				}
			}
		}
	}

	allOfTableCache.Add(swaggerPath, allOfTable)
	return allOfTable, nil
}

func Expand(modelName, swaggerPath string) (*Model, error) {
	doc, err := loadSwagger(swaggerPath)
	if err != nil {
		return nil, err
	}

	if modelName == "" {
		return nil, nil
	}

	spec := doc.Spec()

	modelSchema, ok := spec.Definitions[modelName]
	if !ok {
		return nil, fmt.Errorf("%s not found in the definition of %s", modelName, swaggerPath)
	}

	output := expandSchema(modelSchema, swaggerPath, modelName, "#", spec, map[string]interface{}{}, map[string]interface{}{})

	output.SourceFile = swaggerPath

	return output, nil
}

func expandSchema(input openapiSpec.Schema, swaggerPath, modelName, identifier string, root interface{}, resolvedDiscriminator map[string]interface{}, resolvedModel map[string]interface{}) *Model {
	output := Model{Identifier: identifier}

	if _, ok := resolvedModel[modelName]; ok {
		return &output
	}
	resolvedModel[modelName] = nil

	if len(input.Type) > 0 {
		output.Type = &input.Type[0]
		if *output.Type == "boolean" {
			boolMap := make(map[string]bool)
			boolMap["true"] = false
			boolMap["false"] = false

			output.Bool = &boolMap
		}
	}

	if input.AdditionalProperties != nil {
		output.HasAdditionalProperties = true
	}

	if input.Format != "" {
		output.Format = &input.Format
	}

	if input.ReadOnly {
		output.IsReadOnly = input.ReadOnly
	}

	if input.Enum != nil {
		enumMap := make(map[string]bool)
		for _, v := range input.Enum {
			switch t := v.(type) {
			case string:
				enumMap[t] = false
			case float64:
				enumMap[fmt.Sprintf("%v", t)] = false
			case int:
				enumMap[fmt.Sprintf("%v", t)] = false
			default:
				log.Printf("[ERROR] unknown enum type %T", t)
				enumMap[fmt.Sprintf("%v", t)] = false
			}
		}

		output.Enum = &enumMap
	}

	properties := make(map[string]*Model)

	// expand ref
	if input.Ref.String() != "" {
		resolved, err := openapiSpec.ResolveRefWithBase(root, &input.Ref, &openapiSpec.ExpandOptions{RelativeBase: swaggerPath})
		if err != nil {
			log.Fatalf("[ERROR] resolve ref %s from %s: %v", input.Ref.String(), swaggerPath, err)
		}

		modelName, refSwaggerPath := SchemaNamePathFromRef(swaggerPath, input.Ref)
		if refSwaggerPath != swaggerPath {
			doc, err := loadSwagger(refSwaggerPath)
			if err != nil {
				log.Fatalf("[ERROR] load swagger %s: %v", refSwaggerPath, err)
			}

			root = doc.Spec()
		}

		referenceModel := expandSchema(*resolved, refSwaggerPath, modelName, identifier, root, resolvedDiscriminator, resolvedModel)
		if referenceModel.Properties != nil {
			for k, v := range *referenceModel.Properties {
				properties[k] = v
			}
		}
		if referenceModel.Enum != nil {
			output.Enum = referenceModel.Enum
		}
		if referenceModel.Type != nil {
			output.Type = referenceModel.Type
		}
		if referenceModel.Format != nil {
			output.Format = referenceModel.Format
		}
		if referenceModel.HasAdditionalProperties {
			output.HasAdditionalProperties = true
		}
		if referenceModel.Bool != nil {
			output.Bool = referenceModel.Bool
		}
		if referenceModel.IsReadOnly {
			output.IsReadOnly = referenceModel.IsReadOnly
		}
		if referenceModel.IsRequired {
			output.IsRequired = referenceModel.IsRequired
		}
		if referenceModel.Discriminator != nil {
			output.Discriminator = referenceModel.Discriminator
		}
		if referenceModel.Variants != nil {
			output.Variants = referenceModel.Variants
		}
		if referenceModel.Item != nil {
			output.Item = referenceModel.Item
		}
	}

	// expand properties
	for k, v := range input.Properties {
		properties[k] = expandSchema(v, swaggerPath, fmt.Sprintf("%s.%s", modelName, k), identifier+"."+k, root, resolvedDiscriminator, resolvedModel)
	}

	// expand composition
	for _, v := range input.AllOf {
		allOf := expandSchema(v, swaggerPath, fmt.Sprintf("%s.allOf", modelName), identifier, root, resolvedDiscriminator, resolvedModel)
		if allOf.Properties != nil {
			for k, v := range *allOf.Properties {
				properties[k] = v
			}
		}

		// the model should be a variant if its allOf contains a discriminator
		if allOf.Discriminator != nil {
			output.Discriminator = allOf.Discriminator
		}
	}

	if len(properties) > 0 {
		for _, v := range input.Required {
			if p, ok := properties[v]; ok {
				p.IsRequired = true
			} else {
				log.Printf("[WARN] required property %s not found in %s", v, modelName)
			}
		}

		// check if all properties are readonly
		allReadOnly := true
		for _, v := range properties {
			if !v.IsReadOnly {
				allReadOnly = false
				break
			}
		}
		if allReadOnly {
			output.IsReadOnly = true
		}

		output.Properties = &properties
	}

	// expand items
	if input.Items != nil {
		item := expandSchema(*input.Items.Schema, swaggerPath, fmt.Sprintf("%s[]", modelName), identifier+"[]", root, resolvedDiscriminator, resolvedModel)
		output.Item = item
	}

	delete(resolvedModel, modelName)

	// expand variants
	if input.Discriminator != "" || output.Discriminator != nil {
		if _, hasResolvedDiscriminator := resolvedDiscriminator[modelName]; !hasResolvedDiscriminator {
			allOfTable, err := getAllOfTable(swaggerPath)
			if err != nil {
				log.Fatalf("[ERROR] get variant table %s: %v", swaggerPath, err)
			}

			varSet, ok := allOfTable[modelName]
			if ok {
				resolvedDiscriminator[modelName] = nil
				variants := map[string]*Model{
					modelName: nil,
				}

				// level order traverse to find all variants
				for len(varSet) > 0 {
					tempVarSet := make(map[string]interface{})
					for variantModelName := range varSet {
						schema := root.(*openapiSpec.Swagger).Definitions[variantModelName]
						variantName := variantModelName
						if variantNameRaw, ok := schema.Extensions[msExtensionDiscriminator]; ok && variantNameRaw != nil {
							variantName = variantNameRaw.(string)
						}

						resolved := expandSchema(schema, swaggerPath, variantModelName, identifier+"{"+variantName+"}", root, resolvedDiscriminator, resolvedModel)
						variants[variantName] = resolved
						if varVarSet, ok := allOfTable[variantModelName]; ok {
							for v := range varVarSet {
								tempVarSet[v] = nil
							}
						}
					}
					varSet = tempVarSet
				}
				delete(resolvedDiscriminator, modelName)
				if input.Discriminator != "" {
					output.Discriminator = &input.Discriminator
				}
				output.Variants = &variants
			}
		}
	}

	return &output
}

func SchemaNamePathFromRef(swaggerPath string, ref openapiSpec.Ref) (name string, path string) {
	url := ref.GetURL()
	if url == nil {
		return "", ""
	}

	path = url.Path
	if path == "" {
		path = swaggerPath
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(swaggerPath), path)
		path = strings.Replace(path, "https:/", "https://", 1)
	}

	fragments := strings.Split(url.Fragment, "/")
	return fragments[len(fragments)-1], path
}
