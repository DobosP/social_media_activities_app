//go:build connections_view_pg

package web

import (
 "context"
 "encoding/json"
 "html"
 "net/http"
 "net/http/httptest"
 "net/url"
 "reflect"
 "strconv"
 "strings"
 "testing"

 "github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
 "github.com/DobosP/social_media_activities_app/services/server/internal/platform"
 "github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
 "github.com/jackc/pgx/v5"
)

type connectionObservedDB struct{delegate platform.Querier;calls []connectionTestQuery}
func(d *connectionObservedDB)Query(ctx context.Context,query string,args ...any)(pgx.Rows,error){d.calls=append(d.calls,connectionTestQuery{query,args});return d.delegate.Query(ctx,query,args...)}
func(d *connectionObservedDB)QueryRow(ctx context.Context,query string,args ...any)pgx.Row{d.calls=append(d.calls,connectionTestQuery{query,args});return d.delegate.QueryRow(ctx,query,args...)}

func connectionPageRead(t *testing.T,mux *http.ServeMux,actor platform.Actor,locale string)*httptest.ResponseRecorder{
 t.Helper();request:=httptest.NewRequest(http.MethodGet,"https://fixture.local/connections/",nil);request.AddCookie(&http.Cookie{Name:"django_language",Value:locale});request=platform.WithActor(request,actor);writer:=httptest.NewRecorder();mux.ServeHTTP(writer,request);if writer.Code!=200||writer.Header().Get("Content-Type")!="text/html; charset=utf-8"||!strings.Contains(writer.Header().Get("Content-Security-Policy"),"'nonce-"){t.Fatalf("registered Connections GET: status%d",writer.Code)};return writer
}
func connectionPagePost(t *testing.T,mux *http.ServeMux,actor platform.Actor,target string,cookie *http.Cookie,accept string)*httptest.ResponseRecorder{
 t.Helper();values:=url.Values{"csrfmiddlewaretoken":{cookie.Value}};if accept!=""{values.Set("accept",accept)};request:=httptest.NewRequest(http.MethodPost,"https://fixture.local"+target,strings.NewReader(values.Encode()));request.Header.Set("Content-Type","application/x-www-form-urlencoded");request.Header.Set("Origin","https://fixture.local");request.AddCookie(cookie);request=platform.WithActor(request,actor);writer:=httptest.NewRecorder();mux.ServeHTTP(writer,request);return writer
}

func TestConnectionsPageActualServiceContextAndRegisteredHTTP(t *testing.T){
 // This new opt-in native lane is required-positive. The ordinary fixture's
 // no-DSN skip is never reached; absent explicit fixture fails this tagged test.
 if *webDomainDSN==""{t.Fatal("explicit disposable web fixture required")}
 s,actor,_,_,mux:=webCasePortFixture(t);s.Config.SPA=false;s.Renderer.CSPEnforce=true;ctx:=context.Background()
 for _,locale:=range []string{"en","ro"}{t.Run("empty/"+locale,func(t *testing.T){response:=connectionPageRead(t,mux,actor,locale);if strings.Contains(response.Body.String(),`class="mini-avatar"`){t.Fatal("empty accepted list emitted row")}})}
 goodA:=testdb.Actor(t,s.DB,"connections-view-a","adult");goodB:=testdb.Actor(t,s.DB,"connections-view-b","adult");blocked:=testdb.Actor(t,s.DB,"connections-view-blocked","adult");minor:=testdb.Actor(t,s.DB,"connections-view-minor","child");inactive:=testdb.Actor(t,s.DB,"connections-view-inactive","adult");incoming:=testdb.Actor(t,s.DB,"connections-view-incoming","adult");outgoing:=testdb.Actor(t,s.DB,"connections-view-outgoing","adult")
 for id,name:=range map[int64]string{goodA.ID:"Alpha <&>",goodB.ID:"Beta <&>"}{if _,err:=s.DB.Exec(ctx,"UPDATE accounts_user SET display_name=$2 WHERE id=$1",id,name);err!=nil{t.Fatal(err)}}
 if _,err:=s.DB.Exec(ctx,"UPDATE accounts_user SET is_active=false WHERE id=$1",inactive.ID);err!=nil{t.Fatal(err)}
 for _,peer:=range []platform.Actor{goodA,goodB,blocked,minor,inactive,actor}{if _,err:=s.DB.Exec(ctx,`INSERT INTO connections_connection(requester_id,addressee_id,status,created_at,decided_at) VALUES($1,$2,'accepted',now(),now())`,actor.ID,peer.ID);err!=nil{t.Fatal("synthetic accepted source fixture",err)}}
 if _,err:=s.DB.Exec(ctx,`INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`,blocked.ID,actor.ID);err!=nil{t.Fatal(err)}
 const incomingID int64=9007199254740993
 const outgoingID int64=9223372036854775807
 for _,edge:=range []struct{id,from,to int64}{{incomingID,incoming.ID,actor.ID},{outgoingID,actor.ID,outgoing.ID}}{if _,err:=s.DB.Exec(ctx,`INSERT INTO connections_connection(id,requester_id,addressee_id,status,created_at,decided_at) VALUES($1,$2,$3,'pending',now(),NULL)`,edge.id,edge.from,edge.to);err!=nil{t.Fatal("synthetic pending source fixture",err)}}
 accepted,err:=s.Social.Connections(ctx,actor);if err!=nil||len(accepted)!=2{t.Fatal("source accepted exclusions",err)};pending,err:=s.Social.PendingConnections(ctx,actor);if err!=nil||len(pending["incoming"])!=1||len(pending["outgoing"])!=1{t.Fatal("source actual pending rows",err)}
 beforeAccepted,_:=json.Marshal(accepted);beforePending,_:=json.Marshal(pending);observed:=&connectionObservedDB{delegate:s.DB};projected,err:=connectionsPageContext(ctx,observed,accepted,pending["incoming"],pending["outgoing"]);if err!=nil||len(observed.calls)!=3{t.Fatal("bounded real source/avatar projection",err)}
 users:=projected["connections"].([]map[string]any);allowed:=map[string]int64{goodA.PublicID:goodA.ID,goodB.PublicID:goodB.ID};expectedPublic:=[]string{};expectedIDs:=[]int64{}
 for _,user:=range users{publicID,ok:=user["public_id"].(string);id,known:=allowed[publicID];if !ok||!known||len(user)!=3{t.Fatal("forbidden identity or internal field entered context")};want,err:=accounts.Avatar(ctx,s.DB,id);if err!=nil||user["avatar_uri"]!=want||!connectionAvatarURI(want){t.Fatal("actual peer/base avatar mismatch",err)};expectedPublic=append(expectedPublic,publicID);expectedIDs=append(expectedIDs,id)}
 if !reflect.DeepEqual(observed.calls[0].args,[]any{expectedPublic})||!reflect.DeepEqual(observed.calls[1].args,[]any{expectedIDs})||!reflect.DeepEqual(observed.calls[2].args,[]any{expectedIDs}){t.Fatal("excluded identities queried or bounded order changed")}
 afterAccepted,_:=json.Marshal(accepted);afterPending,_:=json.Marshal(pending);if string(beforeAccepted)!=string(afterAccepted)||string(beforePending)!=string(afterPending){t.Fatal("Service JSON/API values changed")}
 for _,locale:=range []string{"en","ro"}{t.Run("present/"+locale,func(t *testing.T){
  response:=connectionPageRead(t,mux,actor,locale);body:=response.Body.String();for _,name:=range []string{"Alpha <&>","Beta <&>"}{if !strings.Contains(body,html.EscapeString(name))||strings.Contains(body,name){t.Fatal("native accepted name escaping")}}
  for _,peer:=range []platform.Actor{blocked,minor,inactive}{if strings.Contains(body,peer.PublicID)||strings.Contains(body,peer.DisplayName){t.Fatal("source excluded peer leaked")}}
  if strings.Count(body,`class="mini-avatar" src="data:image/svg+xml;base64,`)!=2||strings.Contains(body,`src=""`){t.Fatal("actual nonempty accepted avatar URI missing")}
  for _,user:=range users{if !strings.Contains(body,`src="`+user["avatar_uri"].(string)+`"`)||!strings.Contains(body,`href="`+routeURL("person",user["public_id"])+`"`){t.Fatal("actual peer image/person join")}}
  if strings.Count(body,`action="/connections/9007199254740993/respond/"`)!=2||strings.Count(body,`action="/connections/9223372036854775807/withdraw/"`)!=1{t.Fatal("source int64 action URL rounded")}
  var cookie *http.Cookie;for _,value:=range response.Result().Cookies(){if value.Name=="csrftoken"{cookie=value}};if cookie==nil||len(cookie.Value)!=52||!strings.Contains(body,`value="`+cookie.Value+`"`){t.Fatal("real source CSRF cookie/form mismatch")}
 })}
 // Real view values join the same Service rows; no manually supplied page data.
 request:=platform.WithActor(httptest.NewRequest(http.MethodGet,"https://fixture.local/connections/",nil),actor);data,template,err:=s.view(request,actor,"connections");if err!=nil||template!="web/connections.html"||!reflect.DeepEqual(data,projected){t.Fatal("actual view differs from complete source projection",err)}
 // Registered route parsing and existing action code must roundtrip the exact
 // high IDs. Decline/withdraw avoid changing the consent/acceptance policy.
 response:=connectionPageRead(t,mux,actor,"en");var cookie *http.Cookie;for _,value:=range response.Result().Cookies(){if value.Name=="csrftoken"{cookie=value}};if cookie==nil{t.Fatal("source CSRF absent")}
 for _,test:=range []struct{id int64;route,accept,status string}{{incomingID,"connection_respond","0","declined"},{outgoingID,"connection_withdraw","","withdrawn"}}{target:=routeURL(test.route,test.id);if !strings.Contains(target,strconv.FormatInt(test.id,10)){t.Fatal("exact source action path")};result:=connectionPagePost(t,mux,actor,target,cookie,test.accept);if result.Code!=302||result.Header().Get("Location")!="/connections/"{t.Fatalf("registered high-ID action status%d",result.Code)};var state string;if err:=s.DB.QueryRow(ctx,"SELECT status FROM connections_connection WHERE id=$1",test.id).Scan(&state);err!=nil||state!=test.status{t.Fatal("exact DB action identity/status",err)}}
 t.Log("connections-page:actual-Service/view/registeredHTTP;en-ro-empty-present;safe-peer-avatars;3queries;blocked-inactive-crosscohort-self-excluded;int64-high-action-roundtrip")
}

func TestConnectionsPageEmptyAvatarProjectionHasNoQueries(t *testing.T){
 if *webDomainDSN==""{t.Fatal("explicit disposable web fixture required")};s,actor,_,_,_:=webCasePortFixture(t);rows,err:=s.Social.Connections(context.Background(),actor);if err!=nil||len(rows)!=0{t.Fatal("actual empty Service query",err)};observed:=&connectionObservedDB{delegate:s.DB};value,err:=connectionsPageContext(context.Background(),observed,rows,nil,nil);if err!=nil||len(observed.calls)!=0||len(value["connections"].([]map[string]any))!=0{t.Fatal("actual empty view projection queried avatars",err)}
}
