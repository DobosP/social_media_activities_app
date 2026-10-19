#!/usr/bin/env python3
"""Fresh group-detail overlay and exact tool/docs-only descendant source bridge."""
import argparse
import json
import os
from pathlib import Path
import re
import stat
import subprocess

PRODUCT_INPUTS = ['services/server/internal/web/routes.json', 'services/server/internal/web/forms.json', 'services/server/internal/web/social_pages.go', 'services/server/internal/web/social_threads.go', 'services/server/internal/web/social_actions.go', 'services/server/internal/web/actions.go', 'services/server/internal/web/server.go', 'services/server/internal/web/router.go', 'services/server/internal/web/renderer.go', 'services/server/internal/web/forms.go', 'services/server/internal/social/groups.go', 'services/server/internal/social/threads.go', 'services/server/internal/social/authorization.go', 'services/server/internal/social/service.go', 'services/server/internal/social/logistics.go', 'services/server/internal/social/http.go', 'services/server/internal/platform/platform.go', 'apps/web/templates/web/group_detail.html', 'apps/web/templates/web/_post.html', 'apps/web/templates/web/_activity_card.html', 'templates/base.html', 'static/js/site.js', 'static/js/hovercard.js', 'static/js/presend-nudge.js', 'static/js/thread-chat.js', 'services/server/internal/web/social_pages_test.go', 'services/server/internal/web/thread_case_port4_test.go', 'services/server/internal/web/policy_thread_test.go', 'services/server/internal/social/social_test.go', 'services/server/internal/social/rate_policy_test.go', 'services/server/internal/social/rest_thread_access_case_test.go', 'services/server/internal/app/group_native_http_test.go', 'apps/social/tests/test_groups.py', 'apps/social/tests/test_public_listing_api.py', 'apps/social/tests/test_group_ask_organiser.py', 'apps/web/tests/test_group_thread_sentiment.py', 'services/server/internal/web/gui_public_get_walk_test.go', 'services/server/internal/web/gui_preferences_get_transport_test.go', 'services/server/internal/web/group_create_custody_test.go', 'services/server/internal/web/group_create_source_guard_test.go', 'services/server/internal/web/family_custody_test.go', 'services/server/internal/web/testdata/gui-public-get-walk/source.json', 'services/server/internal/web/markup.go', 'apps/web/templates/web/_post_sentiment.html', 'services/server/internal/web/spa_models.go', 'services/server/internal/web/family.go', 'services/server/internal/web/group_create.go', 'services/server/internal/web/post_notice.go', 'services/server/internal/web/media_busy.go', 'services/server/internal/accounts/avatar.go', 'services/server/internal/avatars/render.go', 'services/server/internal/social/sentiment.go', 'services/server/internal/media/attachments.go', 'services/server/internal/media/service.go', 'services/server/internal/schema/baseline.sql', 'services/server/internal/app/app.go', 'services/server/internal/app/group_detail_native_http_test.go', 'services/server/internal/web/group_detail.go', 'services/server/internal/web/group_detail_copy.go', 'services/server/internal/web/group_detail_custody_test.go', 'services/server/internal/web/group_detail_post.go', 'services/server/internal/web/group_detail_projection.go', 'services/server/internal/web/group_detail_test.go', 'services/server/internal/web/pages/group-detail-card.gohtml', 'services/server/internal/web/pages/group-detail-post.gohtml', 'services/server/internal/web/pages/group-detail.gohtml', 'static/css/group-detail-native.css', 'services/server/internal/web/group_detail_ask_compat_test.go', 'services/server/internal/web/group_detail_config_schema_test.go', 'services/server/internal/app/group_detail_ask_native_http_test.go', 'services/server/internal/app/group_detail_capture_test.go', 'services/server/go.mod', 'services/server/go.sum', 'tools/gui-native-family/qualify.py', 'tools/gui-native-family/db-entry.sh', 'tools/gui-native-family/db-binding.json', 'tools/gui-native-family/test_qualify.py', 'services/server/tools/gui-native-family-bootstrap/main.go', 'tools/gui-native-group-detail/extract-projection.py', 'tools/gui-native-family/ui/fixture-export_test.go.txt', 'tools/gui-native-group/fixture-export_test.go.txt', 'tools/gui-native-group/family-html-baseline.json', 'tools/gui-native-family/ui/color-stability.mjs', 'services/server/internal/web/family_projection_test.go', 'services/server/internal/web/family_benchmark_test.go']
UI_INPUTS = ['tools/gui-native-group-detail/prepare-ui-overlay.py', 'tools/gui-native-group-detail/fixture-export_test.go.txt', 'tools/gui-native-group-detail/desktop-qa.mjs', 'tools/gui-native-group-detail/qualify.py', 'tools/gui-native-group-detail/README.md', 'tools/gui-native-group-detail/prior-html-baseline.json']

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--repo', type=Path, required=True)
    parser.add_argument('--producer-source', required=True)
    parser.add_argument('--output', type=Path)
    parser.add_argument('--check-bridge', action='store_true')
    args = parser.parse_args()
    repo, output = args.repo, args.output
    if not (__debug__ and repo.is_absolute() and repo.resolve() == repo
            and Path(__file__).absolute() == repo/'tools/gui-native-group-detail/prepare-ui-overlay.py'
            and re.fullmatch('[0-9a-f]{40}', args.producer_source)):
        raise SystemExit('Canonical committed method/repo and complete PG producer identity required')
    names = PRODUCT_INPUTS + UI_INPUTS
    for name in names:
        file = repo/name
        if file.resolve() != file or not stat.S_ISREG(file.lstat().st_mode):
            raise SystemExit('Regular unaliased method/product input required: '+name)
    subprocess.run(['git','-C',str(repo),'ls-files','--error-unmatch','--',*names],check=True,stdout=subprocess.DEVNULL)
    if subprocess.check_output(['git','-C',str(repo),'status','--porcelain']):
        raise SystemExit('Clean committed source required')
    subprocess.run(['git','-C',str(repo),'merge-base','--is-ancestor',args.producer_source,'HEAD'],check=True)
    allowed = set(UI_INPUTS) | {'STATUS.md','docs/execplans/gui-native-group-detail.md'}
    changed = [name.decode() for name in subprocess.check_output(['git','-C',str(repo),'diff','--no-renames','--name-only','-z',args.producer_source,'HEAD']).split(b'\0') if name]
    if not set(changed) <= allowed:
        raise SystemExit('Change outside declared UI methods/docs; product/permission/capture inputs must remain exact: '+repr(changed))
    head = subprocess.check_output(['git','-C',str(repo),'rev-parse','HEAD']).decode().strip()
    proof = {'producer_source':args.producer_source,'ui_head':head,'UI_only_changed_paths':changed,'source_paths':names,'product_paths':PRODUCT_INPUTS,'scope':'Complete PG producer plus exact UI-tool/docs descendant; source rows are partial references, full Git source manifest is the closure'}
    if args.check_bridge:
        if output is not None:raise SystemExit('Bridge-only mode writes no overlay')
        print(json.dumps(proof));return
    if not (output is not None and output.is_absolute() and output.parent.resolve()==output.parent
            and output.parent.is_dir() and output.parent.stat().st_uid==os.getuid()
            and '_temp' in output.parts and not output.is_relative_to(repo)):
        raise SystemExit('Fresh output in an existing owned task scratch parent required')
    config={'Replace':{
        str(repo/'services/server/internal/web/group_detail_ui_qa_overlay_test.go'):str(repo/'tools/gui-native-group-detail/fixture-export_test.go.txt'),
        str(repo/'services/server/internal/web/group_ui_qa_overlay_test.go'):str(repo/'tools/gui-native-group/fixture-export_test.go.txt'),
        str(repo/'services/server/internal/web/family_ui_qa_overlay_test.go'):str(repo/'tools/gui-native-family/ui/fixture-export_test.go.txt'),
    }}
    for virtual in config['Replace']:
        target=Path(virtual)
        if target.exists() or target.is_symlink():
            raise SystemExit('QA overlay must add an absent virtual source, never replace a real file: '+virtual)
    fd=os.open(output,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
    with os.fdopen(fd,'w') as file:file.write(json.dumps(config,indent=2)+'\n')
    print(json.dumps({'output':str(output),**proof}))

if __name__=='__main__':main()
