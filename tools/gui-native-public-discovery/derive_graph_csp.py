"""Derive local CSP assets from the existing, byte-pinned vendor bundle.

No JavaScript execution, package install, network or source-bundle mutation.
Two eager literal CSS-injection expressions are removed; their existing final
semicolons and every other JavaScript byte/notice remain in original order.
"""
from pathlib import Path
import argparse,base64,hashlib,json

SOURCE_PATH=Path('static/js/vendor/3d-force-graph.min.js')
JS_PATH=Path('static/js/vendor/3d-force-graph-csp.min.js')
CSS_PATH=Path('static/css/community-graph-vendor.css')
SOURCE_BYTES=706907
SOURCE_SHA256='0ddfb909b551f4fdeb6b43cca567e71f2bc82b2e2eadf1d7cda65af8961b2d0f'
SOURCE_NOTICE=b'// Version 1.73.4 3d-force-graph - https://github.com/vasturiano/3d-force-graph\n'
SOURCE_NOTICE_SHA256='bf2e53a76910d073e3984031188905a632aba4e205aef70eeea7d3ccf7d1ff82'
STYLE_CALL=b'!function(t,e){void 0===e&&(e={});var n=e.insertAt;if("undefined"!=typeof document){var i=document.head||document.getElementsByTagName("head")[0],r=document.createElement("style");r.type="text/css","top"===n&&i.firstChild?i.insertBefore(r,i.firstChild):i.appendChild(r),r.styleSheet?r.styleSheet.cssText=t:r.appendChild(document.createTextNode(t))}}('
CSS_CSP_SHA256=['0/4q5IwejFb2zgHlQwwtwmGHS8ZbXE1kmz/TkRFlZ7M=','4lIjf9U5mi7aONUVTDDP7JzyfXOtFyueHQaAl/MDJ5U=']
CSS_NOTICE=b'/* Generated literal vendor styles. Original upstream version/source notice and all other bundle bytes remain in 3d-force-graph-csp.min.js. Reproduce with tools/gui-native-public-discovery/derive_graph_csp.py. */\n'

def sha(raw):return hashlib.sha256(raw).hexdigest()
def derive(source):
 if len(source)!=SOURCE_BYTES or sha(source)!=SOURCE_SHA256:raise ValueError('exact original vendor bytes required')
 if not source.startswith(SOURCE_NOTICE) or sha(SOURCE_NOTICE)!=SOURCE_NOTICE_SHA256:raise ValueError('original distribution notice required')
 if source.count(STYLE_CALL)!=2:raise ValueError('exact two eager literal style injections required')
 edits=[];styles=[];offset=0
 for expected_css in CSS_CSP_SHA256:
  start=source.index(STYLE_CALL,offset);literal_start=start+len(STYLE_CALL)
  tail=source[literal_start:].decode('utf-8');style,consumed=json.JSONDecoder().raw_decode(tail)
  if not isinstance(style,str):raise ValueError('literal stylesheet string required')
  literal_end=literal_start+len(tail[:consumed].encode('utf-8'))
  if source[literal_end:literal_end+2]!=b');':raise ValueError('exact style call terminator required')
  style_bytes=style.encode('utf-8');digest=base64.b64encode(hashlib.sha256(style_bytes).digest()).decode('ascii')
  if digest!=expected_css:raise ValueError('literal vendor CSS hash differs')
  # Remove the call through its closing parenthesis; retain the original
  # semicolon as a statement boundary rather than joining neighboring tokens.
  end=literal_end+1
  edits.append({'start':start,'end':end,'removed_sha256':sha(source[start:end]),'removed_bytes':end-start,'css_sha256':sha(style_bytes),'css_bytes':len(style_bytes),'css_csp_sha256':digest})
  styles.append(style_bytes);offset=literal_end+2
 pieces=[];cursor=0
 for edit in edits:pieces.extend([source[cursor:edit['start']]]);cursor=edit['end']
 pieces.append(source[cursor:]);derived=b''.join(pieces)
 if not derived.startswith(SOURCE_NOTICE) or b'document.createElement("style")' in derived:raise ValueError('notice or eager-style removal differs')
 css=CSS_NOTICE+b'\n'.join(styles)+b'\n'
 metadata={'source':{'path':SOURCE_PATH.as_posix(),'bytes':len(source),'sha256':sha(source),'notice_sha256':SOURCE_NOTICE_SHA256},'edits':edits,'derived_js':{'path':JS_PATH.as_posix(),'bytes':len(derived),'sha256':sha(derived)},'derived_css':{'path':CSS_PATH.as_posix(),'bytes':len(css),'sha256':sha(css)},'all_bytes_outside_two_removed_calls_retained':True,'all_original_distribution_notices_retained':True,'javascript_executed':False,'original_vendor_modified':False}
 return derived,css,metadata

def main():
 parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--repo',type=Path,required=True);group=parser.add_mutually_exclusive_group(required=True);group.add_argument('--output',type=Path);group.add_argument('--check',action='store_true');args=parser.parse_args()
 if not args.repo.is_absolute() or args.repo.resolve()!=args.repo:raise ValueError('canonical absolute repo required')
 original=(args.repo/SOURCE_PATH).read_bytes();js,css,metadata=derive(original)
 if args.check:
  if (args.repo/JS_PATH).read_bytes()!=js or (args.repo/CSS_PATH).read_bytes()!=css:raise ValueError('committed derived assets differ from exact generator output')
  print(json.dumps({'status':'EXACT_DERIVATION_MATCH','source_sha256':SOURCE_SHA256,'edits':2}));return
 if not args.output.is_absolute() or args.output.resolve()!=args.output:raise ValueError('canonical absolute fresh output required')
 args.output.mkdir(mode=0o700)
 for relative,raw in [(JS_PATH,js),(CSS_PATH,css)]:
  target=args.output/relative;target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(raw)
 (args.output/'derivation.json').write_text(json.dumps(metadata,indent=2)+'\n')
 if (args.repo/SOURCE_PATH).read_bytes()!=original:raise ValueError('original source changed during derivation')
 print(json.dumps({'status':'SOURCE_BYTES_DERIVED_NO_JS_EXECUTION','output':str(args.output),'edits':2,'js_sha256':sha(js),'css_sha256':sha(css)}))
if __name__=='__main__':main()
