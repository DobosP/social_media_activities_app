package web

import (
 "bytes"
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "html"
 "net/http"
 "net/http/httptest"
 "path/filepath"
 "reflect"
 "strconv"
 "strings"
 "testing"

 "github.com/DobosP/social_media_activities_app/services/server/internal/platform"
 "github.com/jackc/pgx/v5"
)

const connectionTestPublicID = "00000000-0000-4000-8000-000000000001"
const connectionTestOtherID = "00000000-0000-4000-8000-000000000002"
func connectionTestUser(id,name string)json.RawMessage{raw,_:=json.Marshal(map[string]any{"public_id":id,"display_name":name});return raw}
func connectionTestPending(id int64)json.RawMessage{raw,_:=json.Marshal(map[string]any{"id":id,"status":"pending","requester":map[string]any{"public_id":connectionTestPublicID,"display_name":"Request <&>"},"addressee":map[string]any{"public_id":connectionTestOtherID,"display_name":"Address <&>"},"created_at":"2026-10-16T12:34:56.123456+00:00"});return raw}

func TestConnectionsContextCompleteTypesOrderAndDetachment(t *testing.T){
 for _,id:=range []int64{1,9007199254740993,9223372036854775807}{t.Run(strconv.FormatInt(id,10),func(t *testing.T){
  accepted:=[]json.RawMessage{connectionTestUser(connectionTestOtherID,"Second"),connectionTestUser(connectionTestPublicID,"First")};pending:=[]json.RawMessage{connectionTestPending(id)}
  context,err:=connectionsContext(accepted,pending,pending);if err!=nil{t.Fatal(err)};users:=context["connections"].([]map[string]any);if len(users)!=2||len(users[0])!=2||users[0]["public_id"]!=connectionTestOtherID||users[1]["display_name"]!="First"{t.Fatal("complete user order or fields changed")}
  incoming:=context["incoming"].([]map[string]any);row:=incoming[0];if len(row)!=6||row["id"]!=id||row["pk"]!=id||row["status"]!="pending"||row["created_at"]!="2026-10-16T12:34:56.123456+00:00"{t.Fatal("pending fields or int64 identity changed")}
  if routeURL("connection_respond",row["pk"])!="/connections/"+strconv.FormatInt(id,10)+"/respond/"||routeURL("connection_withdraw",row["pk"])!="/connections/"+strconv.FormatInt(id,10)+"/withdraw/"{t.Fatal("exact route identity changed")}
  incoming[0]["requester"].(map[string]any)["display_name"]="changed";if context["outgoing"].([]map[string]any)[0]["requester"].(map[string]any)["display_name"]=="changed"{t.Fatal("decoded roots alias")};accepted[0][0]='!';pending[0][0]='!';if users[0]["public_id"]!=connectionTestOtherID||row["id"]!=id{t.Fatal("input bytes alias maps")}
 })}
 empty,err:=connectionsContext(nil,[]json.RawMessage{},nil);if err!=nil||len(empty)!=3{t.Fatal("empty context",err)};for _,name:=range []string{"connections","incoming","outgoing"}{rows,ok:=empty[name].([]map[string]any);if !ok||rows==nil||len(rows)!=0{t.Fatal("present empty slice lost")}}
 for _,name:=range []string{"",strings.Repeat("😀",150),"literal �",`slash\uD800`,"\u2028\u2029"}{if _,err:=connectionUserRow(connectionTestUser(connectionTestPublicID,name));err!=nil{t.Fatal("valid source string refused",err)}}
 paired:=[]byte(`{"public_id":"`+connectionTestPublicID+`","display_name":"\ud83d\ude00\ufffd"}`);row,err:=connectionUserRow(paired);if err!=nil||row["display_name"]!="😀�"{t.Fatal("valid Unicode pair or replacement scalar changed",err)}
}

func TestConnectionsContextMalformedRowsAreAtomic(t *testing.T){
 good:=connectionTestPending(1);users:=[]json.RawMessage{connectionTestUser(connectionTestPublicID,"private-row-marker")}
 invalid:=[]string{"",`null`,`[]`,`{}`,`{"id":1}`,string(good)+`{}`,strings.Replace(string(good),`"id":1`,`"id":1,"id":2`,1),strings.Replace(string(good),`"id":1`,`"id":1,"pk":1`,1),strings.Replace(string(good),`"status":"pending"`,`"status":null`,1),strings.Replace(string(good),`"status":"pending"`,`"status":"accepted"`,1),strings.Replace(string(good),`"requester":{`,`"requester":{"unknown":1,`,1),strings.Replace(string(good),`"created_at":"2026-10-16T12:34:56.123456+00:00"`,`"created_at":null`,1)}
 for _,id:=range []string{`"1"`,`0`,`-1`,`-0`,`01`,`1.0`,`1e0`,`1E0`,`9223372036854775808`,`18446744073709551615`,`true`,`null`}{invalid=append(invalid,strings.Replace(string(good),`"id":1`,`"id":`+id,1))}
 // Build malformed nested strings separately; json.Marshal escapes HTML in good.
 for _,name:=range []string{`"\ud800"`,`"\udc00"`,`"\ud800\ud800"`,`"\ud800x"`,`"\ud800\u0041"`,`"\ud800\\udc00"`}{invalid=append(invalid,`{"id":1,"status":"pending","requester":{"public_id":"`+connectionTestPublicID+`","display_name":`+name+`},"addressee":{"public_id":"`+connectionTestOtherID+`","display_name":"ok"},"created_at":"now"}`)}
 invalid=append(invalid,`{"id":1,"status":"pending","requester":{"public_id":"`+connectionTestPublicID+`","display_name":"a","display_\u006eame":"b"},"addressee":{"public_id":"`+connectionTestOtherID+`","display_name":"ok"},"created_at":"now"}`)
 invalid=append(invalid,string(append([]byte(`{"id":1,"status":"`),255)),strings.Repeat(" ",connectionRowBytesLimit)+string(good))
 for index,raw:=range invalid{t.Run(fmt.Sprintf("pending-%d",index),func(t *testing.T){value,err:=connectionsContext(users,nil,[]json.RawMessage{json.RawMessage(raw)});if err==nil||value!=nil{t.Fatal("invalid row yielded partial context")};if strings.Contains(err.Error(),"private-row-marker")||strings.Contains(err.Error(),raw)&&len(raw)>8{t.Fatal("error leaked row content")}})}
 for _,raw:=range [][]byte{[]byte(`{"public_id":"x","display_name":"a","extra":1}`),[]byte(`{"public_id":null,"display_name":"a"}`),[]byte(`{"public_id":"x","display_name":null}`),[]byte(`{"public_id":"x","display_name":1}`),connectionTestUser(connectionTestPublicID,strings.Repeat("x",151)),[]byte(`{"public_id":"x","public_\u0069d":"y","display_name":"a"}`)}{if value,err:=connectionsContext([]json.RawMessage{json.RawMessage(raw)},nil,nil);err==nil||value!=nil{t.Fatal("invalid user accepted")}}
 invalidUTF8:=append([]byte(`{"public_id":"x","display_name":"`),255);invalidUTF8=append(invalidUTF8,[]byte(`"}`)...);if _,err:=connectionUserRow(invalidUTF8);err==nil{t.Fatal("invalid UTF8 was replaced silently")}
 ordinary:=connectionTestUser(connectionTestPublicID,"limit");atLimit:=append(bytes.Repeat([]byte(" "),connectionRowBytesLimit-len(ordinary)),ordinary...);if _,err:=connectionUserRow(atLimit);err!=nil{t.Fatal("exact byte bound refused",err)};if _,err:=connectionUserRow(append(atLimit,' '));err==nil{t.Fatal("oversized row accepted")}
 tooMany:=make([]json.RawMessage,connectionRowsLimit+1);for i:=range tooMany{tooMany[i]=good};if value,err:=connectionsContext(nil,tooMany,nil);err==nil||value!=nil{t.Fatal("source row bound ignored")}
 bound:=make([]json.RawMessage,connectionRowsLimit);for i:=range bound{bound[i]=good};if value,err:=connectionsContext(nil,bound,nil);err!=nil||len(value["incoming"].([]map[string]any))!=connectionRowsLimit{t.Fatal("actual SQL row bound refused",err)}
}

type connectionTestRows struct{pgx.Rows;values [][]any;index int;failure error;closed bool}
func(r *connectionTestRows)Next()bool{if r.index>=len(r.values){return false};r.index++;return true}
func(r *connectionTestRows)Scan(dest ...any)error{if r.index==0||r.index>len(r.values)||len(dest)!=len(r.values[r.index-1]){return errors.New("synthetic scan shape")};for i,value:=range r.values[r.index-1]{switch p:=dest[i].(type){case *int64:v,ok:=value.(int64);if !ok{return errors.New("synthetic int64")};*p=v;case *int:v,ok:=value.(int);if !ok{return errors.New("synthetic int")};*p=v;case *string:v,ok:=value.(string);if !ok{return errors.New("synthetic string")};*p=v;default:return errors.New("synthetic destination")}};return nil}
func(r *connectionTestRows)Close(){r.closed=true}
func(r *connectionTestRows)Err()error{return r.failure}
type connectionTestQuery struct{sql string;args []any}
type connectionTestDB struct{answers []*connectionTestRows;calls []connectionTestQuery;failAt int}
func(d *connectionTestDB)Query(_ context.Context,sql string,args ...any)(pgx.Rows,error){d.calls=append(d.calls,connectionTestQuery{sql,args});if len(d.calls)==d.failAt{return nil,errors.New("private-database-marker")};if len(d.calls)>len(d.answers){return nil,errors.New("extra query")};return d.answers[len(d.calls)-1],nil}
func(d *connectionTestDB)QueryRow(context.Context,string,...any)pgx.Row{panic("unexpected per-user query")}
func connectionAvatarTestDB()*connectionTestDB{return &connectionTestDB{answers:[]*connectionTestRows{{values:[][]any{{int64(11),connectionTestPublicID},{int64(22),connectionTestOtherID}}},{values:[][]any{{int64(11),"one",1,0},{int64(22),"two",1,0}}},{values:[][]any{}}}}}
func TestConnectionsContextAvatarsBoundedAuthorizedJoins(t *testing.T){
 users:=[]map[string]any{{"public_id":connectionTestOtherID,"display_name":"two"},{"public_id":connectionTestPublicID,"display_name":"one"}}
 db:=connectionAvatarTestDB();got,err:=connectionAvatars(context.Background(),db,users);if err!=nil||len(got)!=2||len(db.calls)!=3{t.Fatal("three-query actual avatar helper",err)};if !reflect.DeepEqual(db.calls[0].args,[]any{[]string{connectionTestOtherID,connectionTestPublicID}})||!reflect.DeepEqual(db.calls[1].args,[]any{[]int64{22,11}})||!reflect.DeepEqual(db.calls[2].args,[]any{[]int64{22,11}}){t.Fatal("authorized query ID/order differs")}
 for i,user:=range got{if len(user)!=3||user["public_id"]!=users[i]["public_id"]||user["display_name"]!=users[i]["display_name"]||!connectionAvatarURI(user["avatar_uri"].(string)){t.Fatal("avatar join or private ID exposure")};for _,row:=range db.answers{if !row.closed{t.Fatal("query rows unclosed")}}};got[0]["display_name"]="mutated";if users[0]["display_name"]!="two"{t.Fatal("avatar result aliases input")}
 empty:=&connectionTestDB{};if got,err:=connectionAvatars(context.Background(),empty,nil);err!=nil||len(got)!=0||len(empty.calls)!=0{t.Fatal("empty list queried",err)}
 for _,kind:=range []string{"query1","query2","query3","missing-id","extra-id","duplicate-public","duplicate-id","scan","row-error","missing-avatar","extra-avatar"}{t.Run(kind,func(t *testing.T){db:=connectionAvatarTestDB();switch kind{case "query1":db.failAt=1;case "query2":db.failAt=2;case "query3":db.failAt=3;case "missing-id":db.answers[0].values=db.answers[0].values[:1];case "extra-id":db.answers[0].values=append(db.answers[0].values,[]any{int64(33),"foreign"});case "duplicate-public":db.answers[0].values=append(db.answers[0].values,db.answers[0].values[0]);case "duplicate-id":db.answers[0].values[1][0]=int64(11);case "scan":db.answers[0].values[0][0]="wrong";case "row-error":db.answers[0].failure=errors.New("private-database-marker");case "missing-avatar":db.answers[1].values=db.answers[1].values[:1];case "extra-avatar":db.answers[1].values=append(db.answers[1].values,[]any{int64(33),"foreign",1,0})};got,err:=connectionAvatars(context.Background(),db,users);if err==nil||got!=nil||strings.Contains(err.Error(),"private-database-marker"){t.Fatal("incomplete avatar join or private error escaped")}})}
 duplicate:=[]map[string]any{users[0],users[0]};db=connectionAvatarTestDB();if got,err:=connectionAvatars(context.Background(),db,duplicate);err==nil||got!=nil||len(db.calls)!=0{t.Fatal("duplicate selected user queried")}
 for _,uri:=range []string{"","javascript:alert(1)","https://foreign.invalid/","data:image/svg+xml;base64,","data:image/svg+xml;base64,!!!!"}{if connectionAvatarURI(uri){t.Fatal("unsafe avatar URI accepted")}}
 db=connectionAvatarTestDB();if got,err:=connectionsPageContext(context.Background(),db,[]json.RawMessage{connectionTestUser(connectionTestPublicID,"one")},nil,[]json.RawMessage{json.RawMessage(`{}`)});err==nil||got!=nil||len(db.calls)!=0{t.Fatal("bad later array triggered enrichment or partial context")}
}

func TestConnectionsContextActualRendererRowsLocalesAndExactActions(t *testing.T){
 root,err:=filepath.Abs("../../../..");if err!=nil{t.Fatal(err)};renderer:=NewRenderer(root);renderer.CSPEnforce=true
 ownAvatars,err:=connectionAvatars(context.Background(),connectionAvatarTestDB(),[]map[string]any{{"public_id":connectionTestPublicID,"display_name":"one"},{"public_id":connectionTestOtherID,"display_name":"two"}});if err!=nil{t.Fatal(err)}
 for _,locale:=range []string{"en","ro"}{for _,selection:=range []string{"empty","accepted","incoming","outgoing","mixed"}{for _,id:=range []int64{1,9007199254740993,9223372036854775807}{t.Run(locale+"/"+selection+"/"+strconv.FormatInt(id,10),func(t *testing.T){
  var accepted,incoming,outgoing []json.RawMessage;if selection=="accepted"||selection=="mixed"{accepted=[]json.RawMessage{connectionTestUser(connectionTestOtherID,"Two <&>"),connectionTestUser(connectionTestPublicID,"One <&>")}};if selection=="incoming"||selection=="mixed"{incoming=[]json.RawMessage{connectionTestPending(id)}};if selection=="outgoing"||selection=="mixed"{outgoing=[]json.RawMessage{connectionTestPending(id)}}
  data,err:=connectionsPageContext(context.Background(),connectionAvatarTestDB(),accepted,incoming,outgoing);if err!=nil{t.Fatal(err)};data["csrf"]="synthetic-renderer-csrf";data["avatar_uri"]=ownAvatars[0]["avatar_uri"];request:=httptest.NewRequest(http.MethodGet,"https://fixture.local/connections/",nil);request.AddCookie(&http.Cookie{Name:"django_language",Value:locale});request=platform.WithActor(request,platform.Actor{ID:1,Username:"synthetic",DisplayName:"Synthetic",Cohort:"adult",Role:"user",IsActive:true});writer:=httptest.NewRecorder();if err:=renderer.Render(writer,request,"web/connections.html",data);err!=nil{t.Fatal("actual original template",err)};if writer.Code!=200||!strings.HasPrefix(writer.Header().Get("Content-Type"),"text/html")||!strings.Contains(writer.Header().Get("Content-Security-Policy"),"'nonce-"){t.Fatal("native response headers")};body:=writer.Body.String()
  if len(accepted)>0{for _,name:=range []string{"One <&>","Two <&>"}{if !strings.Contains(body,html.EscapeString(name))||strings.Contains(body,name){t.Fatal("native name escaping")}};if strings.Count(body,`class="mini-avatar" src="data:image/svg+xml;base64,`)!=2||strings.Contains(body,`src=""`){t.Fatal("source-owned accepted avatar missing")}}
  for _,route:=range []struct{name string;count int}{{"connection_respond",len(incoming)*2},{"connection_withdraw",len(outgoing)}}{want:=`action="`+routeURL(route.name,id)+`"`;if strings.Count(body,want)!=route.count{t.Fatal("exact pending action ID/count",route.name,id)}}
  if len(incoming)+len(outgoing)>0&&!strings.Contains(body,`value="synthetic-renderer-csrf"`){t.Fatal("native form CSRF absent")};if bytes.Contains(writer.Body.Bytes(),[]byte("<nil>")){t.Fatal("invalid pending route identity")}
 })}}}
}
