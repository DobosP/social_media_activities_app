# ADR-0068: Native display and access preference pages

Date: 2026-10-20
Status: implemented and independently qualified as a bounded functional feature; main/milestone/production remain separate.

## Decision

Rebuild the existing `/display/` and `/access/` HTML views with a small typed Go
projection,cached html/template and page-scoped CSS. These controls already have
Go handlers and services. This is selected presentation work,not a new settings
engine or a requirement to convert every template one-to-one. Keep current
forms,copy,default behavior,permissions,cookies,CSRF,transactions and audits.

One exact Renderer dispatcher addition selects the two page names; common
nonce/CSP/cache/Vary/response buffering stays intact. Existing templates,base
assets/fonts/site.js,translation catalogues,SPA contracts,services,SQL/schema
and dependencies remain. The projector adds no lookup,mutation or authority.

## Source-defined contexts

Display is public. Its AccountView returns before account/guardian service
queries; signed rendering receives the Renderer-default shell without richer
AccountView id/pk/is_active/guardian fields. Anonymous public navigation and
signed-only tabs remain distinct. Theme auto/light/dark/contrast,text
normal/large/larger and motion auto/reduce/full retain existing cookie defaults
and allowlists. Saving each valid field independently,ignoring invalid/missing
fields and redirecting302 to `/display/` remain existing AccountAction behavior.
Cookie attributes/lifetime and existing device-only explanation are unchanged;
no new cookie/legal exemption claim follows.

Preserve the genuine SPA-enabled public `_data=1` display fallback: it falls
back to HTML after the public path skips CSRF issuance,so its rendered token
may be empty. Rendering must not impose a new nonempty-token invariant. Every
POST still uses existing CSRF/Origin admission; the view authorizes no save.

Access remains active self-only. Existing AccountView/API supplies exactly
needs_step_free,needs_accessible_toilet,needs_hearing_loop and prefers_quiet.
Existing action converts on/true/1 to true,other/missing values to false,then
sends all four booleans through the self Settings transaction. The audit stays
`user.access_preference_updated`,without preference values. No subject/ward
selector or new authority is added. Unknown accessibility stays visible and
quiet remains saved for future use,not a new matching or clinical rule.

Normal GET enrichment and direct POST-error rendering stay separate. The latter
reloads persisted preferences through current AccountView,escapes feedback and
keeps direct request/default site/canonical/absent-avatar context. It does not
render submitted values as saved or retry a mutation. A failing re-fetch keeps
the existing domain-error response. Access SPA HTML/JSON continues ahead of
native dispatch. Headers/methods/anonymous redirects remain source-defined.

## Preservation and qualification

Core proposal c44afc3c and bridge proposal84340771 are scratch-only inputs. Core
source review82bea746 closes only an EN/RO test-label defect; it is not whole
source,runtime or feature acceptance. The earlier Account/Cont assertion and
review are retained as V1 history. Production core images stayed byte-exact.

Keep all original templates,catalogues,tests/assertions/source pins. Narrowly
named test-only frozen aliases retain original Pongo form/DOM/load predicates;
new ordinary registered App/UI tests exercise current native behavior. No
production reference bypass or legacy fallback is introduced. Require exact
whole-image inverses/current physical-source gates and genuine unique mutation
targets before application. Do not skip roots,widen caps or relax rules.

After actual language closingC is known,bind its exact beforeimages/full Git
frame and all45 accepted previous body identities. Independently review the
whole composed packet; only then apply,run appropriate focused race/vet/format/
docs checks,commit the exact producer and preflight its complete Git archive
under the unchanged cap. Run unchanged complete affected PG7,then independently
review actual inventories/source/cleanup before desktop operational admission.

Desktop1280x720 JS/no-JS,strict whole-document Axe,real Tab/FormData,current
assets and all45 literal prior bodies require actual results/review. No mobile
or device testing is requested. Any performance measurement is separate; Go
templates do not imply a speedup. Preserve failures honestly and one verified
rolling setup/compact proof;no nested workspace/canonical archive or deletion
is authorized by this presentation decision.

No additional child policy,clinical content,legal/identity authority,main/GUI
milestone/production/provider/government/real-child activation is included.

Source binding: actual language closing `2750eb5874508bb5f87d93ca4701e5dc9d7b3501` (tree `8a89aab9d1a0acd3a1e821515fcd82b1d2030217`), metadata-only over M016c; no product beforeimage changed. Whole-packet admission and runtime remain pending.

## Applied-source iterative checkpoint

Applied native display/access presentation passes iterative focused race checks:35roots/127named passed,including all9new web roots;one pre-existing DB root was unexecuted/skipped without a DSN. Vet web+app,format,whitespace and docs67 pass. Full affectedPG7 must still execute that root and all5new App roots with zero skips;desktop/45body qualification pending. No new performance or milestone acceptance.

## Actual bounded qualification

Native display/access bounded functional feature independently accepted463bf0e1 on exactS e0065ca9 and desktopT2 d2cd4a88;see [current display/access record](../reviews/gui-native-display-access/README.md). ActualUI4984f789/reviewb3526ecf:8JS8noJS/21unique strictAxe462passing/0violations-incomplete/114keyboard/45literalpriorbodies/94artifacts/ownedcleanup. Firstfdf9266c remainsFAIL;nonqualifyingdiagnostic2fc04ed2 and source-onlyV3 repairs do not replace actualUI. BenchmarkNOT_REQUESTED. Footer spacing and existingROcatalogue fallback remain explicit polish work. Four canonical metadata files reference original proof in place;no new archive or further deletion. No new authority or milestone acceptance.

Earlier pending/iterative checkpoints above remain history;the actual fullPG run executed the previously skipped no-DSN root and all five new App roots with zero skips.
