"""Regenerate embedded OpenAPI and the public paths in the dashboard contract.
Requires PyYAML. Run from any directory: python scripts/generate-public-admin.py
"""
from pathlib import Path
from copy import deepcopy
import json
import re
import textwrap
import yaml
root=Path(__file__).resolve().parents[1]
spec=yaml.safe_load((root/'contracts/public-admin.openapi.yaml').read_text(encoding='utf-8'))
registered=re.findall(r'\{"((?:GET|POST|PUT|PATCH|DELETE) /[^" ]+)",\s*"([^"]*)",\s*"([^"]+)"\}',(root/'services/internal/api/public_admin.go').read_text(encoding='utf-8'))
expected={pattern:permission for pattern,permission,_ in registered}
actual={method.upper()+' '+path:op['x-permission'] for path,methods in spec['paths'].items() for method,op in methods.items()}
assert expected.keys()==actual.keys(),'Public route registry and contract differ'
for pattern,permission in expected.items():assert actual[pattern]==(permission or 'Valid admin token'),pattern
(root/'services/internal/api/public_openapi.json').write_text(json.dumps(spec,indent=2,ensure_ascii=False)+'\n',encoding='utf-8')
main=root/'contracts/openapi.yaml'
text=main.read_text(encoding='utf-8')
prefix,tail=text.split('\n  /api/admin/v1/openapi.json:',1)
parsed=yaml.safe_load(textwrap.dedent('  /api/admin/v1/openapi.json:'+tail))
paths={}
def external_refs(value):
 if isinstance(value,dict):
  return {key:('./public-admin.openapi.yaml'+v if key=='$ref' and isinstance(v,str) and v.startswith('#/components/schemas/') else external_refs(v)) for key,v in value.items()}
 if isinstance(value,list):return [external_refs(v) for v in value]
 return value
for path,methods in spec['paths'].items():
 paths['/api/admin/v1'+path]=external_refs(deepcopy(methods))
 for operation in paths['/api/admin/v1'+path].values():operation['security']=[{'adminToken':[]}]
for path,methods in parsed.items():
 if path.startswith('/api/settings/admin-tokens') or path=='/api/admin-onboarding/{token}':paths[path]=methods
class PublicDumper(yaml.SafeDumper):
 def generate_anchor(self,node):return 'public_admin_'+super().generate_anchor(node)
main.write_text(prefix+'\n'+''.join('  '+line+'\n' if line else '\n' for line in yaml.dump(paths,Dumper=PublicDumper,sort_keys=False,allow_unicode=True).splitlines()),encoding='utf-8')
print(f'Updated {len(actual)} public operations and embedded OpenAPI.')
