package brokerapi_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/pivotal-cf/brokerapi"
	"github.com/pivotal-cf/brokerapi/fakes"

	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/lager/lagertest"
	"github.com/drewolson/testflight"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Service Broker API", func() {
	var fakeServiceBroker *fakes.FakeServiceBroker
	var brokerAPI http.Handler
	var brokerLogger *lagertest.TestLogger
	var credentials = brokerapi.BrokerCredentials{
		Username: "username",
		Password: "password",
	}

	makeInstanceProvisioningRequest := func(instanceID string, details map[string]interface{}, queryString string) *testflight.Response {
		response := &testflight.Response{}

		testflight.WithServer(brokerAPI, func(r *testflight.Requester) {
			path := "/v2/service_instances/" + instanceID + queryString

			buffer := &bytes.Buffer{}
			json.NewEncoder(buffer).Encode(details)
			request, err := http.NewRequest("PUT", path, buffer)
			Expect(err).NotTo(HaveOccurred())
			request.Header.Add("Content-Type", "application/json")
			request.SetBasicAuth(credentials.Username, credentials.Password)

			response = r.Do(request)
		})
		return response
	}

	makeInstanceProvisioningRequestWithAcceptsIncomplete := func(instanceID string, details map[string]interface{}, acceptsIncomplete bool) *testflight.Response {
		var acceptsIncompleteFlag string

		if acceptsIncomplete {
			acceptsIncompleteFlag = "?accepts_incomplete=true"
		} else {
			acceptsIncompleteFlag = "?accepts_incomplete=false"
		}

		return makeInstanceProvisioningRequest(instanceID, details, acceptsIncompleteFlag)
	}

	lastLogLine := func() lager.LogFormat {
		noOfLogLines := len(brokerLogger.Logs())
		if noOfLogLines == 0 {
			// better way to raise error?
			err := errors.New("expected some log lines but there were none")
			Expect(err).NotTo(HaveOccurred())
		}

		return brokerLogger.Logs()[noOfLogLines-1]
	}

	BeforeEach(func() {
		fakeServiceBroker = &fakes.FakeServiceBroker{
			InstanceLimit: 3,
		}
		brokerLogger = lagertest.NewTestLogger("broker-api")
		brokerAPI = brokerapi.New(fakeServiceBroker, brokerLogger, credentials)
	})

	Describe("response headers", func() {
		makeRequest := func() *httptest.ResponseRecorder {
			recorder := httptest.NewRecorder()
			request, _ := http.NewRequest("GET", "/v2/catalog", nil)
			request.SetBasicAuth(credentials.Username, credentials.Password)
			brokerAPI.ServeHTTP(recorder, request)
			return recorder
		}

		It("has a Content-Type header", func() {
			response := makeRequest()

			header := response.Header().Get("Content-Type")
			Ω(header).Should(Equal("application/json"))
		})
	})

	Describe("authentication", func() {
		makeRequestWithoutAuth := func() *testflight.Response {
			response := &testflight.Response{}
			testflight.WithServer(brokerAPI, func(r *testflight.Requester) {
				request, _ := http.NewRequest("GET", "/v2/catalog", nil)
				response = r.Do(request)
			})
			return response
		}

		makeRequestWithAuth := func(username string, password string) *testflight.Response {
			response := &testflight.Response{}
			testflight.WithServer(brokerAPI, func(r *testflight.Requester) {
				request, _ := http.NewRequest("GET", "/v2/catalog", nil)
				request.SetBasicAuth(username, password)

				response = r.Do(request)
			})
			return response
		}

		makeRequestWithUnrecognizedAuth := func() *testflight.Response {
			response := &testflight.Response{}
			testflight.WithServer(brokerAPI, func(r *testflight.Requester) {
				request, _ := http.NewRequest("GET", "/v2/catalog", nil)
				// dXNlcm5hbWU6cGFzc3dvcmQ= is base64 encoding of 'username:password',
				// ie, a correctly encoded basic authorization header
				request.Header["Authorization"] = []string{"NOTBASIC dXNlcm5hbWU6cGFzc3dvcmQ="}

				response = r.Do(request)
			})
			return response
		}

		It("returns 401 when the authorization header has an incorrect password", func() {
			response := makeRequestWithAuth("username", "fake_password")
			Expect(response.StatusCode).To(Equal(401))
		})

		It("returns 401 when the authorization header has an incorrect username", func() {
			response := makeRequestWithAuth("fake_username", "password")
			Expect(response.StatusCode).To(Equal(401))
		})

		It("returns 401 when there is no authorization header", func() {
			response := makeRequestWithoutAuth()
			Expect(response.StatusCode).To(Equal(401))
		})

		It("returns 401 when there is a unrecognized authorization header", func() {
			response := makeRequestWithUnrecognizedAuth()
			Expect(response.StatusCode).To(Equal(401))
		})

		It("does not call through to the service broker when not authenticated", func() {
			makeRequestWithAuth("username", "fake_password")
			Ω(fakeServiceBroker.BrokerCalled).ShouldNot(BeTrue(),
				"broker should not have been hit when authentication failed",
			)
		})
	})

	Describe("catalog endpoint", func() {
		makeCatalogRequest := func() *testflight.Response {
			response := &testflight.Response{}
			testflight.WithServer(brokerAPI, func(r *testflight.Requester) {
				request, _ := http.NewRequest("GET", "/v2/catalog", nil)
				request.SetBasicAuth("username", "password")

				response = r.Do(request)
			})
			return response
		}

		It("returns a 200", func() {
			response := makeCatalogRequest()
			Expect(response.StatusCode).To(Equal(200))
		})

		It("returns valid catalog json", func() {
			response := makeCatalogRequest()
			Expect(response.Body).To(MatchJSON(fixture("catalog.json")))
		})
	})

	Describe("instance lifecycle endpoint", func() {
		makeInstanceDeprovisioningRequest := func(instanceID, queryString string) *testflight.Response {
			response := &testflight.Response{}
			testflight.WithServer(brokerAPI, func(r *testflight.Requester) {
				path := fmt.Sprintf("/v2/service_instances/%s?plan_id=plan-id&service_id=service-id", instanceID)
				if queryString != "" {
					path = fmt.Sprintf("%s&%s", path, queryString)
				}
				request, err := http.NewRequest("DELETE", path, strings.NewReader(""))
				Expect(err).NotTo(HaveOccurred())
				request.Header.Add("Content-Type", "application/json")
				request.SetBasicAuth("username", "password")

				response = r.Do(request)

			})
			return response
		}

		Describe("provisioning", func() {
			var instanceID string
			var provisionDetails map[string]interface{}

			BeforeEach(func() {
				instanceID = uniqueInstanceID()
				provisionDetails = map[string]interface{}{
					"service_id":        "service-id",
					"plan_id":           "plan-id",
					"organization_guid": "organization-guid",
					"space_guid":        "space-guid",
				}
			})

			It("calls Provision on the service broker with all params", func() {
				makeInstanceProvisioningRequest(instanceID, provisionDetails, "")
				Expect(fakeServiceBroker.ProvisionDetails).To(Equal(brokerapi.ProvisionDetails{
					ServiceID:        "service-id",
					PlanID:           "plan-id",
					OrganizationGUID: "organization-guid",
					SpaceGUID:        "space-guid",
				}))
			})

			It("calls Provision on the service broker with the instance id", func() {
				makeInstanceProvisioningRequest(instanceID, provisionDetails, "")
				Expect(fakeServiceBroker.ProvisionedInstanceIDs).To(ContainElement(instanceID))
			})

			Context("when the broker returns some operation data", func() {
				BeforeEach(func() {
					fakeServiceBroker = &fakes.FakeServiceBroker{
						InstanceLimit:         3,
						OperationDataToReturn: "some-operation-data",
					}
					fakeAsyncServiceBroker := &fakes.FakeAsyncServiceBroker{
						FakeServiceBroker:    *fakeServiceBroker,
						ShouldProvisionAsync: true,
					}
					brokerAPI = brokerapi.New(fakeAsyncServiceBroker, brokerLogger, credentials)
				})

				It("returns the operation data to the cloud controller", func() {
					resp := makeInstanceProvisioningRequest(instanceID, provisionDetails, "")
					Expect(resp.Body).To(MatchJSON(fixture("operation_data_response.json")))
				})
			})

			Context("when there are arbitrary params", func() {
				BeforeEach(func() {
					provisionDetails["parameters"] = map[string]interface{}{
						"string": "some-string",
						"number": 1,
						"object": struct{ Name string }{"some-name"},
						"array":  []interface{}{"a", "b", "c"},
					}
				})

				It("calls Provision on the service broker with all params", func() {
					rawParams := `{
						"string":"some-string",
						"number":1,
						"object": { "Name": "some-name" },
						"array": [ "a", "b", "c" ]
					}`

					makeInstanceProvisioningRequest(instanceID, provisionDetails, "")
					Expect(string(fakeServiceBroker.ProvisionDetails.RawParameters)).To(MatchJSON(rawParams))
				})
			})

			Context("when the instance does not exist", func() {
				It("returns a 201", func() {
					response := makeInstanceProvisioningRequest(instanceID, provisionDetails, "")
					Expect(response.StatusCode).To(Equal(201))
				})

				It("returns empty json", func() {
					response := makeInstanceProvisioningRequest(instanceID, provisionDetails, "")
					Expect(response.Body).To(MatchJSON(fixture("provisioning.json")))
				})

				Context("when the broker returns a dashboard URL", func() {
					BeforeEach(func() {
						fakeServiceBroker.DashboardURL = "some-dashboard-url"
					})

					It("returns json with dasboard URL", func() {
						response := makeInstanceProvisioningRequest(instanceID, provisionDetails, "")
						Expect(response.Body).To(MatchJSON(fixture("provisioning_with_dashboard.json")))
					})
				})

				Context("when the instance limit has been reached", func() {
					BeforeEach(func() {
						for i := 0; i < fakeServiceBroker.InstanceLimit; i++ {
							makeInstanceProvisioningRequest(uniqueInstanceID(), provisionDetails, "")
						}
					})

					It("returns a 500", func() {
						response := makeInstanceProvisioningRequest(instanceID, provisionDetails, "")
						Expect(response.StatusCode).To(Equal(500))
					})

					It("returns json with a description field and a useful error message", func() {
						response := makeInstanceProvisioningRequest(instanceID, provisionDetails, "")
						Expect(response.Body).To(MatchJSON(fixture("instance_limit_error.json")))
					})

					It("logs an appropriate error", func() {
						makeInstanceProvisioningRequest(instanceID, provisionDetails, "")

						Expect(lastLogLine().Message).To(ContainSubstring("provision.instance-limit-reached"))
						Expect(lastLogLine().Data["error"]).To(ContainSubstring("instance limit for this service has been reached"))
					})
				})

				Context("when an unexpected error occurs", func() {
					BeforeEach(func() {
						fakeServiceBroker.ProvisionError = errors.New("broker failed")
					})

					It("returns a 500", func() {
						response := makeInstanceProvisioningRequest(instanceID, provisionDetails, "")
						Expect(response.StatusCode).To(Equal(500))
					})

					It("returns json with a description field and a useful error message", func() {
						response := makeInstanceProvisioningRequest(instanceID, provisionDetails, "")
						Expect(response.Body).To(MatchJSON(`{"description":"broker failed"}`))
					})

					It("logs an appropriate error", func() {
						makeInstanceProvisioningRequest(instanceID, provisionDetails, "")
						Expect(lastLogLine().Message).To(ContainSubstring("provision.unknown-error"))
						Expect(lastLogLine().Data["error"]).To(ContainSubstring("broker failed"))
					})
				})

				Context("RawParameters are not valid JSON", func() {
					BeforeEach(func() {
						fakeServiceBroker.ProvisionError = brokerapi.ErrRawParamsInvalid
					})

					It("returns a 422", func() {
						response := makeInstanceProvisioningRequest(instanceID, provisionDetails, "")
						Expect(response.StatusCode).To(Equal(422))
					})

					It("returns json with a description field and a useful error message", func() {
						response := makeInstanceProvisioningRequest(instanceID, provisionDetails, "")
						Expect(response.Body).To(MatchJSON(`{"description":"The format of the parameters is not valid JSON"}`))
					})

					It("logs an appropriate error", func() {
						makeInstanceProvisioningRequest(instanceID, provisionDetails, "")
						Expect(lastLogLine().Message).To(ContainSubstring("provision.invalid-raw-params"))
						Expect(lastLogLine().Data["error"]).To(ContainSubstring("The format of the parameters is not valid JSON"))
					})
				})

				Context("when we send invalid json", func() {
					makeBadInstanceProvisioningRequest := func(instanceID string) *testflight.Response {
						response := &testflight.Response{}

						testflight.WithServer(brokerAPI, func(r *testflight.Requester) {
							path := "/v2/service_instances/" + instanceID

							body := strings.NewReader("{{{{{")
							request, err := http.NewRequest("PUT", path, body)
							Expect(err).NotTo(HaveOccurred())
							request.Header.Add("Content-Type", "application/json")
							request.SetBasicAuth(credentials.Username, credentials.Password)

							response = r.Do(request)
						})

						return response
					}

					It("returns a 422 bad request", func() {
						response := makeBadInstanceProvisioningRequest(instanceID)
						Expect(response.StatusCode).Should(Equal(422))
					})

					It("logs a message", func() {
						makeBadInstanceProvisioningRequest(instanceID)
						Expect(lastLogLine().Message).To(ContainSubstring("provision.invalid-service-details"))
					})
				})
			})

			Context("when the instance already exists", func() {
				BeforeEach(func() {
					makeInstanceProvisioningRequest(instanceID, provisionDetails, "")
				})

				It("returns a 409", func() {
					response := makeInstanceProvisioningRequest(instanceID, provisionDetails, "")
					Expect(response.StatusCode).To(Equal(409))
				})

				It("returns an empty JSON object", func() {
					response := makeInstanceProvisioningRequest(instanceID, provisionDetails, "")
					Expect(response.Body).To(MatchJSON(`{}`))
				})

				It("logs an appropriate error", func() {
					makeInstanceProvisioningRequest(instanceID, provisionDetails, "")
					Expect(lastLogLine().Message).To(ContainSubstring("provision.instance-already-exists"))
					Expect(lastLogLine().Data["error"]).To(ContainSubstring("instance already exists"))
				})
			})

			Describe("accepts_incomplete", func() {
				Context("when the accepts_incomplete flag is true", func() {
					It("calls ProvisionAsync on the service broker", func() {
						acceptsIncomplete := true
						makeInstanceProvisioningRequestWithAcceptsIncomplete(instanceID, provisionDetails, acceptsIncomplete)
						Expect(fakeServiceBroker.ProvisionDetails).To(Equal(brokerapi.ProvisionDetails{
							ServiceID:        "service-id",
							PlanID:           "plan-id",
							OrganizationGUID: "organization-guid",
							SpaceGUID:        "space-guid",
						}))

						Expect(fakeServiceBroker.ProvisionedInstanceIDs).To(ContainElement(instanceID))
					})

					Context("when the broker chooses to provision asynchronously", func() {
						BeforeEach(func() {
							fakeServiceBroker = &fakes.FakeServiceBroker{
								InstanceLimit: 3,
							}
							fakeAsyncServiceBroker := &fakes.FakeAsyncServiceBroker{
								FakeServiceBroker:    *fakeServiceBroker,
								ShouldProvisionAsync: true,
							}
							brokerAPI = brokerapi.New(fakeAsyncServiceBroker, brokerLogger, credentials)
						})

						It("returns a 202", func() {
							response := makeInstanceProvisioningRequestWithAcceptsIncomplete(instanceID, provisionDetails, true)
							Expect(response.StatusCode).To(Equal(http.StatusAccepted))
						})
					})

					Context("when the broker chooses to provision synchronously", func() {
						BeforeEach(func() {
							fakeServiceBroker = &fakes.FakeServiceBroker{
								InstanceLimit: 3,
							}
							fakeAsyncServiceBroker := &fakes.FakeAsyncServiceBroker{
								FakeServiceBroker:    *fakeServiceBroker,
								ShouldProvisionAsync: false,
							}
							brokerAPI = brokerapi.New(fakeAsyncServiceBroker, brokerLogger, credentials)
						})

						It("returns a 201", func() {
							response := makeInstanceProvisioningRequestWithAcceptsIncomplete(instanceID, provisionDetails, true)
							Expect(response.StatusCode).To(Equal(http.StatusCreated))
						})
					})
				})

				Context("when the accepts_incomplete flag is false", func() {
					It("returns a 201", func() {
						response := makeInstanceProvisioningRequestWithAcceptsIncomplete(instanceID, provisionDetails, false)
						Expect(response.StatusCode).To(Equal(http.StatusCreated))
					})

					Context("when broker can only respond asynchronously", func() {
						BeforeEach(func() {
							fakeServiceBroker = &fakes.FakeServiceBroker{
								InstanceLimit: 3,
							}
							fakeAsyncServiceBroker := &fakes.FakeAsyncOnlyServiceBroker{
								*fakeServiceBroker,
							}
							brokerAPI = brokerapi.New(fakeAsyncServiceBroker, brokerLogger, credentials)
						})

						It("returns a 422", func() {
							acceptsIncomplete := false
							response := makeInstanceProvisioningRequestWithAcceptsIncomplete(instanceID, provisionDetails, acceptsIncomplete)
							Expect(response.StatusCode).To(Equal(422))
							Expect(response.Body).To(MatchJSON(fixture("async_required.json")))
						})
					})
				})

				Context("when the accepts_incomplete flag is missing", func() {
					It("returns a 201", func() {
						response := makeInstanceProvisioningRequest(instanceID, provisionDetails, "")
						Expect(response.StatusCode).To(Equal(http.StatusCreated))
					})

					Context("when broker can only respond asynchronously", func() {
						BeforeEach(func() {
							fakeServiceBroker = &fakes.FakeServiceBroker{
								InstanceLimit: 3,
							}
							fakeAsyncServiceBroker := &fakes.FakeAsyncOnlyServiceBroker{
								*fakeServiceBroker,
							}
							brokerAPI = brokerapi.New(fakeAsyncServiceBroker, brokerLogger, credentials)
						})

						It("returns a 422", func() {
							acceptsIncomplete := false
							response := makeInstanceProvisioningRequestWithAcceptsIncomplete(instanceID, provisionDetails, acceptsIncomplete)
							Expect(response.StatusCode).To(Equal(422))
							Expect(response.Body).To(MatchJSON(fixture("async_required.json")))
						})
					})
				})
			})
		})

		Describe("updating", func() {
			var (
				instanceID  string
				details     brokerapi.UpdateDetails
				queryString string

				response *testflight.Response
			)

			makeInstanceUpdateRequest := func(instanceID string, details brokerapi.UpdateDetails, queryString string) *testflight.Response {
				response := &testflight.Response{}

				testflight.WithServer(brokerAPI, func(r *testflight.Requester) {
					path := "/v2/service_instances/" + instanceID + queryString

					buffer := &bytes.Buffer{}
					json.NewEncoder(buffer).Encode(details)
					request, err := http.NewRequest("PATCH", path, buffer)
					Expect(err).NotTo(HaveOccurred())
					request.Header.Add("Content-Type", "application/json")
					request.SetBasicAuth(credentials.Username, credentials.Password)

					response = r.Do(request)
				})
				return response
			}

			BeforeEach(func() {
				instanceID = uniqueInstanceID()
				details = brokerapi.UpdateDetails{
					ServiceID:  "some-service-id",
					PlanID:     "new-plan",
					Parameters: map[string]interface{}{"new-param": "new-param-value"},
					PreviousValues: brokerapi.PreviousValues{
						PlanID:    "old-plan",
						ServiceID: "service-id",
						OrgID:     "org-id",
						SpaceID:   "space-id",
					},
				}
			})

			JustBeforeEach(func() {
				response = makeInstanceUpdateRequest(instanceID, details, queryString)
			})

			Context("when the broker returns no error", func() {
				Context("when the broker responds synchronously", func() {
					It("returns HTTP 200", func() {
						Expect(response.StatusCode).To(Equal(http.StatusOK))
					})

					It("returns JSON content type", func() {
						Expect(response.RawResponse.Header.Get("Content-Type")).To(Equal("application/json"))
					})

					It("returns empty JSON body", func() {
						Expect(response.Body).To(Equal("{}\n"))
					})

					It("calls broker with instanceID and update details", func() {
						Expect(fakeServiceBroker.UpdatedInstanceIDs).To(ConsistOf(instanceID))
						Expect(fakeServiceBroker.UpdateDetails).To(Equal(details))
					})

					Context("when accepts_incomplete=true", func() {
						BeforeEach(func() {
							queryString = "?accepts_incomplete=true"
						})

						It("tells broker async is allowed", func() {
							Expect(fakeServiceBroker.AsyncAllowed).To(BeTrue())
						})
					})

					Context("when accepts_incomplete is not supplied", func() {
						BeforeEach(func() {
							queryString = ""
						})

						It("tells broker async not allowed", func() {
							Expect(fakeServiceBroker.AsyncAllowed).To(BeFalse())
						})
					})
				})

				Context("when the broker responds asynchronously", func() {
					BeforeEach(func() {
						fakeServiceBroker.ShouldReturnAsync = true
					})

					It("returns HTTP 202", func() {
						Expect(response.StatusCode).To(Equal(http.StatusAccepted))
					})

					Context("when the broker responds with operation data", func() {
						BeforeEach(func() {
							fakeServiceBroker.OperationDataToReturn = "some-operation-data"
						})

						It("returns the operation data to the cloud controller", func() {
							Expect(response.Body).To(MatchJSON(fixture("operation_data_response.json")))
						})
					})
				})
			})

			Context("when the broker indicates that it needs async support", func() {
				BeforeEach(func() {
					fakeServiceBroker.UpdateError = brokerapi.ErrAsyncRequired
				})

				It("returns HTTP 422", func() {
					Expect(response.StatusCode).To(Equal(422))
				})

				It("returns a descriptive message", func() {
					var body map[string]string
					err := json.Unmarshal([]byte(response.Body), &body)
					Expect(err).ToNot(HaveOccurred())
					Expect(body["error"]).To(Equal("AsyncRequired"))
					Expect(body["description"]).To(Equal("This service plan requires client support for asynchronous service operations."))
				})
			})

			Context("when the broker indicates that the plan cannot be upgraded", func() {
				BeforeEach(func() {
					fakeServiceBroker.UpdateError = brokerapi.ErrPlanChangeNotSupported
				})

				It("returns HTTP 422", func() {
					Expect(response.StatusCode).To(Equal(422))
				})

				It("returns a descriptive message", func() {
					var body map[string]string
					err := json.Unmarshal([]byte(response.Body), &body)
					Expect(err).ToNot(HaveOccurred())
					Expect(body["error"]).To(Equal("PlanChangeNotSupported"))
					Expect(body["description"]).To(Equal("The requested plan migration cannot be performed"))
				})
			})

			Context("when the broker errors in an unknown way", func() {
				BeforeEach(func() {
					fakeServiceBroker.UpdateError = errors.New("some horrible internal error")
				})

				It("returns HTTP 500", func() {
					Expect(response.StatusCode).To(Equal(500))
				})

				It("returns a descriptive message", func() {
					var body map[string]string
					err := json.Unmarshal([]byte(response.Body), &body)
					Expect(err).ToNot(HaveOccurred())
					Expect(body["description"]).To(Equal("some horrible internal error"))
				})
			})
		})

		Describe("deprovisioning", func() {
			It("calls Deprovision on the service broker with the instance id", func() {
				instanceID := uniqueInstanceID()
				makeInstanceDeprovisioningRequest(instanceID, "")
				Expect(fakeServiceBroker.DeprovisionedInstanceIDs).To(ContainElement(instanceID))
			})

			Context("when the instance exists", func() {
				var instanceID string
				var provisionDetails map[string]interface{}

				BeforeEach(func() {
					instanceID = uniqueInstanceID()

					provisionDetails = map[string]interface{}{
						"plan_id":           "plan-id",
						"organization_guid": "organization-guid",
						"space_guid":        "space-guid",
					}
					makeInstanceProvisioningRequest(instanceID, provisionDetails, "")
				})

				itReturnsStatus := func(expectedStatus int, queryString string) {
					It(fmt.Sprintf("returns HTTP %d", expectedStatus), func() {
						response := makeInstanceDeprovisioningRequest(instanceID, queryString)
						Expect(response.StatusCode).To(Equal(expectedStatus))
					})
				}

				itReturnsEmptyJsonObject := func(queryString string) {
					It("returns an empty JSON object", func() {
						response := makeInstanceDeprovisioningRequest(instanceID, queryString)
						Expect(response.Body).To(MatchJSON(`{}`))
					})
				}

				Context("when the broker can only operate synchronously", func() {
					Context("when the accepts_incomplete flag is not set", func() {
						itReturnsStatus(200, "")
						itReturnsEmptyJsonObject("")
					})

					Context("when the accepts_incomplete flag is set to true", func() {
						itReturnsStatus(200, "accepts_incomplete=true")
						itReturnsEmptyJsonObject("accepts_incomplete=true")
					})
				})

				Context("when the broker can only operate asynchronously", func() {
					BeforeEach(func() {
						fakeAsyncServiceBroker := &fakes.FakeAsyncOnlyServiceBroker{
							FakeServiceBroker: *fakeServiceBroker,
						}
						brokerAPI = brokerapi.New(fakeAsyncServiceBroker, brokerLogger, credentials)
					})

					Context("when the accepts_incomplete flag is not set", func() {
						itReturnsStatus(422, "")

						It("returns a descriptive error", func() {
							response := makeInstanceDeprovisioningRequest(instanceID, "")
							Expect(response.Body).To(MatchJSON(fixture("async_required.json")))
						})
					})

					Context("when the accepts_incomplete flag is set to true", func() {
						itReturnsStatus(202, "accepts_incomplete=true")
						itReturnsEmptyJsonObject("accepts_incomplete=true")
					})

					Context("when the broker returns operation data", func() {
						BeforeEach(func() {
							fakeServiceBroker.OperationDataToReturn = "some-operation-data"
							fakeAsyncServiceBroker := &fakes.FakeAsyncOnlyServiceBroker{
								FakeServiceBroker: *fakeServiceBroker,
							}
							brokerAPI = brokerapi.New(fakeAsyncServiceBroker, brokerLogger, credentials)
						})

						itReturnsStatus(202, "accepts_incomplete=true")

						It("returns the operation data to the cloud controller", func() {
							response := makeInstanceDeprovisioningRequest(instanceID, "accepts_incomplete=true")
							Expect(response.Body).To(MatchJSON(fixture("operation_data_response.json")))
						})
					})
				})

				Context("when the broker can operate both synchronously and asynchronously", func() {
					BeforeEach(func() {
						fakeAsyncServiceBroker := &fakes.FakeAsyncServiceBroker{
							FakeServiceBroker: *fakeServiceBroker,
						}
						brokerAPI = brokerapi.New(fakeAsyncServiceBroker, brokerLogger, credentials)
					})

					Context("when the accepts_incomplete flag is not set", func() {
						itReturnsStatus(200, "")
						itReturnsEmptyJsonObject("")
					})

					Context("when the accepts_incomplete flag is set to true", func() {
						itReturnsStatus(202, "accepts_incomplete=true")
						itReturnsEmptyJsonObject("accepts_incomplete=true")
					})
				})

				It("contains plan_id", func() {
					makeInstanceDeprovisioningRequest(instanceID, "")
					Expect(fakeServiceBroker.DeprovisionDetails.PlanID).To(Equal("plan-id"))
				})

				It("contains service_id", func() {
					makeInstanceDeprovisioningRequest(instanceID, "")
					Expect(fakeServiceBroker.DeprovisionDetails.ServiceID).To(Equal("service-id"))
				})
			})

			Context("when the instance does not exist", func() {
				var instanceID string

				It("returns a 410", func() {
					response := makeInstanceDeprovisioningRequest(uniqueInstanceID(), "")
					Expect(response.StatusCode).To(Equal(410))
				})

				It("returns an empty JSON object", func() {
					response := makeInstanceDeprovisioningRequest(uniqueInstanceID(), "")
					Expect(response.Body).To(MatchJSON(`{}`))
				})

				It("logs an appropriate error", func() {
					instanceID = uniqueInstanceID()
					makeInstanceDeprovisioningRequest(instanceID, "")
					Expect(lastLogLine().Message).To(ContainSubstring("deprovision.instance-missing"))
					Expect(lastLogLine().Data["error"]).To(ContainSubstring("instance does not exist"))
				})
			})

			Context("when instance deprovisioning fails", func() {
				var instanceID string
				var provisionDetails map[string]interface{}

				BeforeEach(func() {
					instanceID = uniqueInstanceID()
					provisionDetails = map[string]interface{}{
						"plan_id":           "plan-id",
						"organization_guid": "organization-guid",
						"space_guid":        "space-guid",
					}
					makeInstanceProvisioningRequest(instanceID, provisionDetails, "")
				})

				BeforeEach(func() {
					fakeServiceBroker.DeprovisionError = errors.New("broker failed")
				})

				It("returns a 500", func() {
					response := makeInstanceDeprovisioningRequest(instanceID, "")
					Expect(response.StatusCode).To(Equal(500))
				})

				It("returns json with a description field and a useful error message", func() {
					response := makeInstanceDeprovisioningRequest(instanceID, "")
					Expect(response.Body).To(MatchJSON(`{"description":"broker failed"}`))
				})

				It("logs an appropriate error", func() {
					makeInstanceDeprovisioningRequest(instanceID, "")
					Expect(lastLogLine().Message).To(ContainSubstring("provision.unknown-error"))
					Expect(lastLogLine().Data["error"]).To(ContainSubstring("broker failed"))
				})
			})
		})
	})

	Describe("binding lifecycle endpoint", func() {
		makeBindingRequest := func(instanceID, bindingID string, details *brokerapi.BindDetails) *testflight.Response {
			response := &testflight.Response{}
			testflight.WithServer(brokerAPI, func(r *testflight.Requester) {
				path := fmt.Sprintf("/v2/service_instances/%s/service_bindings/%s",
					instanceID, bindingID)

				buffer := &bytes.Buffer{}

				if details != nil {
					json.NewEncoder(buffer).Encode(details)
				}

				request, err := http.NewRequest("PUT", path, buffer)

				Expect(err).NotTo(HaveOccurred())

				request.Header.Add("Content-Type", "application/json")
				request.SetBasicAuth("username", "password")

				response = r.Do(request)
			})
			return response
		}

		Describe("binding", func() {
			var (
				instanceID string
				bindingID  string
				details    *brokerapi.BindDetails
			)

			BeforeEach(func() {
				instanceID = uniqueInstanceID()
				bindingID = uniqueBindingID()
				details = &brokerapi.BindDetails{
					AppGUID:   "app_guid",
					PlanID:    "plan_id",
					ServiceID: "service_id",
				}
			})

			Context("when the associated instance exists", func() {
				It("calls Bind on the service broker with the instance and binding ids", func() {
					makeBindingRequest(instanceID, bindingID, details)
					Expect(fakeServiceBroker.BoundInstanceIDs).To(ContainElement(instanceID))
					Expect(fakeServiceBroker.BoundBindingIDs).To(ContainElement(bindingID))
					Expect(fakeServiceBroker.BoundBindingDetails).To(Equal(*details))
				})

				It("returns the credentials returned by Bind", func() {
					response := makeBindingRequest(uniqueInstanceID(), uniqueBindingID(), details)
					Expect(response.Body).To(MatchJSON(fixture("binding.json")))
				})

				It("returns a 201", func() {
					response := makeBindingRequest(uniqueInstanceID(), uniqueBindingID(), details)
					Expect(response.StatusCode).To(Equal(201))
				})

				Context("when syslog_drain_url is being passed", func() {
					BeforeEach(func() {
						fakeServiceBroker.SyslogDrainURL = "some-drain-url"
					})

					It("responds with the syslog drain url", func() {
						response := makeBindingRequest(uniqueInstanceID(), uniqueBindingID(), details)
						Expect(response.Body).To(MatchJSON(fixture("binding_with_syslog.json")))
					})
				})

				Context("when route_service_url is being passed", func() {
					BeforeEach(func() {
						fakeServiceBroker.RouteServiceURL = "some-route-url"
					})

					It("responds with the route service url", func() {
						response := makeBindingRequest(uniqueInstanceID(), uniqueBindingID(), details)
						Expect(response.Body).To(MatchJSON(fixture("binding_with_route_service.json")))
					})
				})

				Context("when no bind details are being passed", func() {
					It("returns a 422", func() {
						response := makeBindingRequest(uniqueInstanceID(), uniqueBindingID(), nil)
						Expect(response.StatusCode).To(Equal(422))
					})
				})

				Context("when there are arbitrary params", func() {
					BeforeEach(func() {
						details.Parameters = map[string]interface{}{
							"string": "some-string",
							"number": 1,
							"object": struct{ Name string }{"some-name"},
							"array":  []interface{}{"a", "b", "c"},
						}
					})

					It("calls Bind on the service broker with all params", func() {
						makeBindingRequest(instanceID, bindingID, details)
						Expect(fakeServiceBroker.BoundBindingDetails.Parameters["string"]).To(Equal("some-string"))
						Expect(fakeServiceBroker.BoundBindingDetails.Parameters["number"]).To(Equal(1.0))
						Expect(fakeServiceBroker.BoundBindingDetails.Parameters["array"]).To(Equal([]interface{}{"a", "b", "c"}))
						actual, _ := fakeServiceBroker.BoundBindingDetails.Parameters["object"].(map[string]interface{})
						Expect(actual["Name"]).To(Equal("some-name"))
					})
				})

				Context("when there is a app_guid in the bind_resource", func() {
					BeforeEach(func() {
						details.BindResource = &brokerapi.BindResource{AppGuid: "a-guid"}
					})

					It("calls Bind on the service broker with the bind_resource", func() {
						makeBindingRequest(instanceID, bindingID, details)
						Expect(fakeServiceBroker.BoundBindingDetails.BindResource).NotTo(BeNil())
						Expect(fakeServiceBroker.BoundBindingDetails.BindResource.AppGuid).To(Equal("a-guid"))
						Expect(fakeServiceBroker.BoundBindingDetails.BindResource.Route).To(BeEmpty())
					})
				})

				Context("when there is a route in the bind_resource", func() {
					BeforeEach(func() {
						details.BindResource = &brokerapi.BindResource{Route: "route.cf-apps.com"}
					})

					It("calls Bind on the service broker with the bind_resource", func() {
						makeBindingRequest(instanceID, bindingID, details)
						Expect(fakeServiceBroker.BoundBindingDetails.BindResource).NotTo(BeNil())
						Expect(fakeServiceBroker.BoundBindingDetails.BindResource.Route).To(Equal("route.cf-apps.com"))
						Expect(fakeServiceBroker.BoundBindingDetails.BindResource.AppGuid).To(BeEmpty())
					})
				})
			})

			Context("when the associated instance does not exist", func() {
				var instanceID string

				BeforeEach(func() {
					fakeServiceBroker.BindError = brokerapi.ErrInstanceDoesNotExist
				})

				It("returns a 404", func() {
					response := makeBindingRequest(uniqueInstanceID(), uniqueBindingID(), details)
					Expect(response.StatusCode).To(Equal(404))
				})

				It("returns an error JSON object", func() {
					response := makeBindingRequest(uniqueInstanceID(), uniqueBindingID(), details)
					Expect(response.Body).To(MatchJSON(`{"description":"instance does not exist"}`))
				})

				It("logs an appropriate error", func() {
					instanceID = uniqueInstanceID()
					makeBindingRequest(instanceID, uniqueBindingID(), details)
					Expect(lastLogLine().Message).To(ContainSubstring("bind.instance-missing"))
					Expect(lastLogLine().Data["error"]).To(ContainSubstring("instance does not exist"))
				})
			})

			Context("when the requested binding already exists", func() {
				var instanceID string

				BeforeEach(func() {
					fakeServiceBroker.BindError = brokerapi.ErrBindingAlreadyExists
				})

				It("returns a 409", func() {
					response := makeBindingRequest(uniqueInstanceID(), uniqueBindingID(), details)
					Expect(response.StatusCode).To(Equal(409))
				})

				It("returns an error JSON object", func() {
					response := makeBindingRequest(uniqueInstanceID(), uniqueBindingID(), details)
					Expect(response.Body).To(MatchJSON(`{"description":"binding already exists"}`))
				})

				It("logs an appropriate error", func() {
					instanceID = uniqueInstanceID()
					makeBindingRequest(instanceID, uniqueBindingID(), details)
					makeBindingRequest(instanceID, uniqueBindingID(), details)

					Expect(lastLogLine().Message).To(ContainSubstring("bind.binding-already-exists"))
					Expect(lastLogLine().Data["error"]).To(ContainSubstring("binding already exists"))
				})
			})

			Context("when the binding returns an error", func() {
				BeforeEach(func() {
					fakeServiceBroker.BindError = errors.New("random error")
				})

				It("returns a generic 500 error response", func() {
					response := makeBindingRequest(uniqueInstanceID(), uniqueBindingID(), details)
					Expect(response.StatusCode).To(Equal(500))
					Expect(response.Body).To(MatchJSON(`{"description":"random error"}`))
				})

				It("logs a detailed error message", func() {
					makeBindingRequest(uniqueInstanceID(), uniqueBindingID(), details)

					Expect(lastLogLine().Message).To(ContainSubstring("bind.unknown-error"))
					Expect(lastLogLine().Data["error"]).To(ContainSubstring("random error"))
				})
			})
		})

		Describe("unbinding", func() {
			makeUnbindingRequest := func(instanceID string, bindingID string) *testflight.Response {
				response := &testflight.Response{}
				testflight.WithServer(brokerAPI, func(r *testflight.Requester) {
					path := fmt.Sprintf("/v2/service_instances/%s/service_bindings/%s?plan_id=plan-id&service_id=service-id",
						instanceID, bindingID)
					request, _ := http.NewRequest("DELETE", path, strings.NewReader(""))
					request.Header.Add("Content-Type", "application/json")
					request.SetBasicAuth("username", "password")

					response = r.Do(request)
				})
				return response
			}

			Context("when the associated instance exists", func() {
				var instanceID string
				var provisionDetails map[string]interface{}

				BeforeEach(func() {
					instanceID = uniqueInstanceID()
					provisionDetails = map[string]interface{}{
						"plan_id":           "plan-id",
						"organization_guid": "organization-guid",
						"space_guid":        "space-guid",
					}
					makeInstanceProvisioningRequest(instanceID, provisionDetails, "")
				})

				Context("and the binding exists", func() {
					var bindingID string

					BeforeEach(func() {
						bindingID = uniqueBindingID()
						makeBindingRequest(instanceID, bindingID, &brokerapi.BindDetails{})
					})

					It("returns a 200", func() {
						response := makeUnbindingRequest(instanceID, bindingID)
						Expect(response.StatusCode).To(Equal(200))
					})

					It("returns an empty JSON object", func() {
						response := makeUnbindingRequest(instanceID, bindingID)
						Expect(response.Body).To(MatchJSON(`{}`))
					})

					It("contains plan_id", func() {
						makeUnbindingRequest(instanceID, bindingID)
						Expect(fakeServiceBroker.UnbindingDetails.PlanID).To(Equal("plan-id"))
					})

					It("contains service_id", func() {
						makeUnbindingRequest(instanceID, bindingID)
						Expect(fakeServiceBroker.UnbindingDetails.ServiceID).To(Equal("service-id"))
					})
				})

				Context("but the binding does not exist", func() {
					It("returns a 410", func() {
						response := makeUnbindingRequest(instanceID, "does-not-exist")
						Expect(response.StatusCode).To(Equal(410))
					})

					It("logs an appropriate error message", func() {
						makeUnbindingRequest(instanceID, "does-not-exist")

						Expect(lastLogLine().Message).To(ContainSubstring("bind.binding-missing"))
						Expect(lastLogLine().Data["error"]).To(ContainSubstring("binding does not exist"))
					})
				})
			})

			Context("when the associated instance does not exist", func() {
				var instanceID string

				It("returns a 410", func() {
					response := makeUnbindingRequest(uniqueInstanceID(), uniqueBindingID())
					Expect(response.StatusCode).To(Equal(http.StatusGone))
				})

				It("returns an empty JSON object", func() {
					response := makeUnbindingRequest(uniqueInstanceID(), uniqueBindingID())
					Expect(response.Body).To(MatchJSON(`{}`))
				})

				It("logs an appropriate error", func() {
					instanceID = uniqueInstanceID()
					makeUnbindingRequest(instanceID, uniqueBindingID())

					Expect(lastLogLine().Message).To(ContainSubstring("bind.instance-missing"))
					Expect(lastLogLine().Data["error"]).To(ContainSubstring("instance does not exist"))
				})
			})
		})

		Describe("last_operation", func() {
			makeLastOperationRequest := func(instanceID, operationData string) *testflight.Response {
				response := &testflight.Response{}
				testflight.WithServer(brokerAPI, func(r *testflight.Requester) {
					path := fmt.Sprintf("/v2/service_instances/%s/last_operation", instanceID)
					if operationData != "" {
						path = fmt.Sprintf("%s?operation=%s", path, url.QueryEscape(operationData))
					}

					request, _ := http.NewRequest("GET", path, strings.NewReader(""))
					request.Header.Add("Content-Type", "application/json")
					request.SetBasicAuth("username", "password")

					response = r.Do(request)
				})
				return response
			}

			It("calls the broker with the relevant instance ID", func() {
				instanceID := "instanceID"
				makeLastOperationRequest(instanceID, "")
				Expect(fakeServiceBroker.LastOperationInstanceID).To(Equal(instanceID))
			})

			It("calls the broker with the URL decoded operation data", func() {
				instanceID := "an-instance"
				operationData := `{"foo":"bar"}`
				makeLastOperationRequest(instanceID, operationData)
				Expect(fakeServiceBroker.LastOperationData).To(Equal(operationData))
			})

			It("should return succeeded if the operation completed successfully", func() {
				fakeServiceBroker.LastOperationState = "succeeded"
				fakeServiceBroker.LastOperationDescription = "some description"

				instanceID := "instanceID"
				response := makeLastOperationRequest(instanceID, "")

				logs := brokerLogger.Logs()

				Expect(logs[0].Message).To(ContainSubstring("lastOperation.starting-check-for-operation"))
				Expect(logs[0].Data["instance-id"]).To(ContainSubstring(instanceID))

				Expect(logs[1].Message).To(ContainSubstring("lastOperation.done-check-for-operation"))
				Expect(logs[1].Data["instance-id"]).To(ContainSubstring(instanceID))
				Expect(logs[1].Data["state"]).To(ContainSubstring(string(fakeServiceBroker.LastOperationState)))

				Expect(response.StatusCode).To(Equal(200))
				Expect(response.Body).To(MatchJSON(fixture("last_operation_succeeded.json")))
			})

			It("should return a 404 and log in case the instance id is not found", func() {
				fakeServiceBroker.LastOperationError = brokerapi.ErrInstanceDoesNotExist
				instanceID := "non-existing"
				response := makeLastOperationRequest(instanceID, "")

				Expect(lastLogLine().Message).To(ContainSubstring("lastOperation.instance-missing"))
				Expect(lastLogLine().Data["error"]).To(ContainSubstring("instance does not exist"))

				Expect(response.StatusCode).To(Equal(404))
				Expect(response.Body).To(MatchJSON(`{"description": "instance does not exist"}`))
			})

			It("should return an internal sever error for all other errors", func() {
				fakeServiceBroker.LastOperationError = errors.New("Blah")
				response := makeLastOperationRequest("instanceID", "")

				Expect(lastLogLine().Message).To(ContainSubstring("lastOperation.unknown-error"))
				Expect(lastLogLine().Data["error"]).To(ContainSubstring("Blah"))

				Expect(response.StatusCode).To(Equal(500))
				Expect(response.Body).To(MatchJSON(`{"description": "Blah"}`))
			})
		})
	})
})
