package tui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportWSDL11GeneratesSOAPRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.Header.Get("SOAPAction") != `"urn:lookup"` {
			http.Error(response, "bad SOAP request", http.StatusBadRequest)
			return
		}
		response.Header().Set("Content-Type", "text/xml")
		_, _ = response.Write([]byte(`<LookupResponse><found>true</found></LookupResponse>`))
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "books.wsdl")
	document := fmt.Sprintf(`<?xml version="1.0"?>
<definitions xmlns="http://schemas.xmlsoap.org/wsdl/"
 xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
 xmlns:soap12="http://schemas.xmlsoap.org/wsdl/soap12/"
 xmlns:xsd="http://www.w3.org/2001/XMLSchema"
 xmlns:tns="urn:books" targetNamespace="urn:books" name="Books">
 <types><xsd:schema targetNamespace="urn:books">
  <xsd:element name="LookupRequest"><xsd:complexType><xsd:sequence>
   <xsd:element name="isbn" type="xsd:string"/>
   <xsd:element name="includeMetadata" type="xsd:boolean"/>
  </xsd:sequence></xsd:complexType></xsd:element>
 </xsd:schema></types>
 <message name="LookupInput"><part name="parameters" element="tns:LookupRequest"/></message>
 <portType name="BooksPortType"><operation name="Lookup"><documentation>Look up a book</documentation><input message="tns:LookupInput"/></operation></portType>
 <binding name="BooksSoap11" type="tns:BooksPortType">
  <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
  <operation name="Lookup"><soap:operation soapAction="urn:lookup"/><input><soap:body use="literal"/></input></operation>
 </binding>
 <binding name="BooksSoap12" type="tns:BooksPortType">
  <soap12:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
  <operation name="Lookup"><soap12:operation soapAction="urn:lookup12"/><input><soap12:body use="literal"/></input></operation>
 </binding>
 <service name="BooksService">
  <port name="Soap11" binding="tns:BooksSoap11"><soap:address location="%s"/></port>
  <port name="Soap12" binding="tns:BooksSoap12"><soap12:address location="https://soap12.example.test/books"/></port>
 </service>
</definitions>`, server.URL)
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	count, err := m.ImportWSDL(path)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || len(m.savedRequests) != 2 {
		t.Fatalf("WSDL import = count %d requests %#v", count, m.savedRequests)
	}
	requests := make(map[string]savedRequest, 2)
	for _, request := range m.savedRequests {
		requests[request.name] = request
	}
	soap11 := requests["BooksService / Soap11 / Lookup"]
	if soap11.url != server.URL || soap11.method != http.MethodPost || soap11.body.mode != bodyRaw || soap11.body.rawType != rawXML || soap11.headers[0].value != "text/xml; charset=utf-8" || soap11.headers[1].value != `"urn:lookup"` {
		t.Fatalf("SOAP 1.1 request = %#v", soap11)
	}
	if !strings.Contains(soap11.body.raw, `<tns:LookupRequest>`) || !strings.Contains(soap11.body.raw, `<tns:isbn>{{isbn}}</tns:isbn>`) || !strings.Contains(soap11.body.raw, `<tns:includeMetadata>{{includeMetadata}}</tns:includeMetadata>`) {
		t.Fatalf("SOAP envelope = %s", soap11.body.raw)
	}
	soap12 := requests["BooksService / Soap12 / Lookup"]
	if !strings.Contains(soap12.headers[0].value, `application/soap+xml`) || !strings.Contains(soap12.headers[0].value, `action="urn:lookup12"`) || len(soap12.headers) != 1 || !strings.Contains(soap12.body.raw, "http://www.w3.org/2003/05/soap-envelope") {
		t.Fatalf("SOAP 1.2 request = %#v", soap12)
	}

	m.applySavedRequest(soap11)
	response := m.DoRequest()().(responseMsg)
	if response.statusCode != http.StatusOK || !strings.Contains(response.responseRaw, "LookupResponse") {
		t.Fatalf("generated SOAP request failed: %#v", response)
	}
}

func TestImportWSDLIsAtomicOnUnsupportedDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.wsdl")
	if err := os.WriteFile(path, []byte(`<description xmlns="http://www.w3.org/ns/wsdl"/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	m.savedRequests = []savedRequest{{name: "existing"}}
	if _, err := m.ImportWSDL(path); err == nil || !strings.Contains(err.Error(), "WSDL 1.1") {
		t.Fatalf("unsupported WSDL error = %v", err)
	}
	if len(m.savedRequests) != 1 || m.savedRequests[0].name != "existing" {
		t.Fatalf("failed WSDL import mutated collection: %#v", m.savedRequests)
	}
}
