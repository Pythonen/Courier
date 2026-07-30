package tui

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"sort"
	"strings"
)

type wsdlDefinitions struct {
	XMLName         xml.Name
	Name            string         `xml:"name,attr"`
	TargetNamespace string         `xml:"targetNamespace,attr"`
	Messages        []wsdlMessage  `xml:"message"`
	PortTypes       []wsdlPortType `xml:"portType"`
	Bindings        []wsdlBinding  `xml:"binding"`
	Services        []wsdlService  `xml:"service"`
	Schemas         []wsdlSchema   `xml:"types>schema"`
}

type wsdlMessage struct {
	Name  string     `xml:"name,attr"`
	Parts []wsdlPart `xml:"part"`
}

type wsdlPart struct {
	Name    string `xml:"name,attr"`
	Element string `xml:"element,attr"`
	Type    string `xml:"type,attr"`
}

type wsdlPortType struct {
	Name       string                  `xml:"name,attr"`
	Operations []wsdlPortTypeOperation `xml:"operation"`
}

type wsdlPortTypeOperation struct {
	Name          string            `xml:"name,attr"`
	Documentation string            `xml:"documentation"`
	Input         wsdlPortTypeInput `xml:"input"`
}

type wsdlPortTypeInput struct {
	Message string `xml:"message,attr"`
}

type wsdlBinding struct {
	Name       string                 `xml:"name,attr"`
	Type       string                 `xml:"type,attr"`
	Extensions []wsdlBindingExtension `xml:"binding"`
	Operations []wsdlBindingOperation `xml:"operation"`
}

type wsdlBindingExtension struct {
	XMLName   xml.Name
	Style     string `xml:"style,attr"`
	Transport string `xml:"transport,attr"`
}

type wsdlBindingOperation struct {
	Name       string                 `xml:"name,attr"`
	Extensions []wsdlSOAPOperation    `xml:"operation"`
	Input      wsdlBindingOperationIO `xml:"input"`
}

type wsdlSOAPOperation struct {
	XMLName xml.Name
	Action  string `xml:"soapAction,attr"`
}

type wsdlBindingOperationIO struct {
	Bodies []wsdlSOAPBody `xml:"body"`
}

type wsdlSOAPBody struct {
	XMLName   xml.Name
	Use       string `xml:"use,attr"`
	Namespace string `xml:"namespace,attr"`
}

type wsdlService struct {
	Name  string     `xml:"name,attr"`
	Ports []wsdlPort `xml:"port"`
}

type wsdlPort struct {
	Name      string        `xml:"name,attr"`
	Binding   string        `xml:"binding,attr"`
	Addresses []wsdlAddress `xml:"address"`
}

type wsdlAddress struct {
	XMLName  xml.Name
	Location string `xml:"location,attr"`
}

type wsdlSchema struct {
	TargetNamespace string            `xml:"targetNamespace,attr"`
	Elements        []wsdlXMLElement  `xml:"element"`
	ComplexTypes    []wsdlComplexType `xml:"complexType"`
}

type wsdlXMLElement struct {
	Name        string          `xml:"name,attr"`
	Type        string          `xml:"type,attr"`
	ComplexType wsdlComplexType `xml:"complexType"`
}

type wsdlComplexType struct {
	Name       string           `xml:"name,attr"`
	Sequence   []wsdlXMLElement `xml:"sequence>element"`
	Extensions []wsdlExtension  `xml:"complexContent>extension"`
}

type wsdlExtension struct {
	Sequence []wsdlXMLElement `xml:"sequence>element"`
}

type wsdlEndpoint struct {
	service string
	port    string
	url     string
	soap12  bool
}

// ImportWSDL appends SOAP requests generated from a WSDL 1.1 document.
func (m *model) ImportWSDL(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read WSDL document: %w", err)
	}
	var definitions wsdlDefinitions
	if err := xml.Unmarshal(data, &definitions); err != nil {
		return 0, fmt.Errorf("decode WSDL document: %w", err)
	}
	if definitions.XMLName.Local != "definitions" {
		return 0, fmt.Errorf("unsupported WSDL root %q; expected WSDL 1.1 definitions", definitions.XMLName.Local)
	}
	generated, err := generateWSDLRequests(definitions)
	if err != nil {
		return 0, err
	}
	m.savedRequests = append(m.savedRequests, generated...)
	return len(generated), nil
}

func generateWSDLRequests(definitions wsdlDefinitions) ([]savedRequest, error) {
	endpoints := make(map[string]wsdlEndpoint)
	for _, service := range definitions.Services {
		for _, port := range service.Ports {
			if len(port.Addresses) == 0 || strings.TrimSpace(port.Addresses[0].Location) == "" {
				continue
			}
			address := port.Addresses[0]
			endpoints[qNameLocal(port.Binding)] = wsdlEndpoint{
				service: service.Name, port: port.Name, url: strings.TrimSpace(address.Location), soap12: strings.Contains(strings.ToLower(address.XMLName.Space), "soap12"),
			}
		}
	}
	messages := make(map[string]wsdlMessage, len(definitions.Messages))
	for _, message := range definitions.Messages {
		messages[message.Name] = message
	}
	portTypes := make(map[string]wsdlPortType, len(definitions.PortTypes))
	for _, portType := range definitions.PortTypes {
		portTypes[portType.Name] = portType
	}
	bindings := append([]wsdlBinding(nil), definitions.Bindings...)
	sort.Slice(bindings, func(left, right int) bool { return bindings[left].Name < bindings[right].Name })
	var requests []savedRequest
	for _, binding := range bindings {
		endpoint, ok := endpoints[binding.Name]
		if !ok {
			continue
		}
		soap12 := endpoint.soap12 || wsdlBindingUsesSOAP12(binding)
		portType := portTypes[qNameLocal(binding.Type)]
		portOperations := make(map[string]wsdlPortTypeOperation, len(portType.Operations))
		for _, operation := range portType.Operations {
			portOperations[operation.Name] = operation
		}
		for _, operation := range binding.Operations {
			action := ""
			if len(operation.Extensions) > 0 {
				action = operation.Extensions[0].Action
				soap12 = soap12 || strings.Contains(strings.ToLower(operation.Extensions[0].XMLName.Space), "soap12")
			}
			portOperation := portOperations[operation.Name]
			message := messages[qNameLocal(portOperation.Input.Message)]
			body := wsdlEnvelope(definitions, operation.Name, message, operation.Input, soap12)
			headers := []headerEntry{{key: "Content-Type", value: wsdlContentType(soap12, action)}}
			if !soap12 && action != "" {
				headers = append(headers, headerEntry{key: "SOAPAction", value: `"` + action + `"`})
			}
			nameParts := []string{endpoint.service, endpoint.port, operation.Name}
			for index := range nameParts {
				nameParts[index] = strings.TrimSpace(nameParts[index])
			}
			name := strings.Join(nonEmptyStrings(nameParts), " / ")
			requests = append(requests, savedRequest{
				name: name, method: "POST", url: endpoint.url, headers: headers,
				auth: authConfig{typeID: authNone}, body: bodyConfig{mode: bodyRaw, rawType: rawXML, raw: body},
			})
		}
	}
	if len(requests) == 0 {
		return nil, fmt.Errorf("WSDL document contains no SOAP binding operations with service addresses")
	}
	return requests, nil
}

func wsdlBindingUsesSOAP12(binding wsdlBinding) bool {
	for _, extension := range binding.Extensions {
		if strings.Contains(strings.ToLower(extension.XMLName.Space), "soap12") {
			return true
		}
	}
	return false
}

func wsdlContentType(soap12 bool, action string) string {
	if soap12 {
		value := "application/soap+xml; charset=utf-8"
		if action != "" {
			value += `; action="` + action + `"`
		}
		return value
	}
	return "text/xml; charset=utf-8"
}

func wsdlEnvelope(definitions wsdlDefinitions, operationName string, message wsdlMessage, bindingInput wsdlBindingOperationIO, soap12 bool) string {
	namespace := definitions.TargetNamespace
	if len(bindingInput.Bodies) > 0 && bindingInput.Bodies[0].Namespace != "" {
		namespace = bindingInput.Bodies[0].Namespace
	}
	rootName := operationName
	var fields []string
	if len(message.Parts) == 1 && message.Parts[0].Element != "" {
		rootName = qNameLocal(message.Parts[0].Element)
		fields = wsdlElementFields(definitions.Schemas, rootName)
	} else {
		for _, part := range message.Parts {
			if part.Name != "" {
				fields = append(fields, part.Name)
			}
		}
	}
	if rootName == "" {
		rootName = "Operation"
	}
	var body strings.Builder
	soapNamespace := "http://schemas.xmlsoap.org/soap/envelope/"
	if soap12 {
		soapNamespace = "http://www.w3.org/2003/05/soap-envelope"
	}
	body.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	body.WriteString(`<soapenv:Envelope xmlns:soapenv="` + soapNamespace + `" xmlns:tns="` + xmlEscape(namespace) + `">` + "\n")
	body.WriteString("  <soapenv:Header/>\n  <soapenv:Body>\n")
	body.WriteString("    <tns:" + rootName + ">\n")
	for _, field := range fields {
		body.WriteString("      <tns:" + field + ">{{" + field + "}}</tns:" + field + ">\n")
	}
	body.WriteString("    </tns:" + rootName + ">\n  </soapenv:Body>\n</soapenv:Envelope>")
	return body.String()
}

func wsdlElementFields(schemas []wsdlSchema, elementName string) []string {
	for _, schema := range schemas {
		complexTypes := make(map[string]wsdlComplexType, len(schema.ComplexTypes))
		for _, complexType := range schema.ComplexTypes {
			complexTypes[complexType.Name] = complexType
		}
		for _, element := range schema.Elements {
			if element.Name != elementName {
				continue
			}
			complexType := element.ComplexType
			if element.Type != "" {
				complexType = complexTypes[qNameLocal(element.Type)]
			}
			fields := complexType.Sequence
			for _, extension := range complexType.Extensions {
				fields = append(fields, extension.Sequence...)
			}
			result := make([]string, 0, len(fields))
			for _, field := range fields {
				if field.Name != "" {
					result = append(result, field.Name)
				}
			}
			return result
		}
	}
	return nil
}

func qNameLocal(value string) string {
	if _, local, ok := strings.Cut(value, ":"); ok {
		return local
	}
	return value
}

func xmlEscape(value string) string {
	var escaped bytes.Buffer
	_ = xml.EscapeText(&escaped, []byte(value))
	return escaped.String()
}

func nonEmptyStrings(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}
