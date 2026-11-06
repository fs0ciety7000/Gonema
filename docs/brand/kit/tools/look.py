import json, subprocess, sys, os
# usage: look.py size bg file.svg...
size=int(sys.argv[1]); bg=sys.argv[2]; files=sys.argv[3:]
jobs=[{'svg':f,'out':os.path.splitext(f)[0]+f'-{size}.png','w':size,'bg':bg} for f in files]
json.dump(jobs,open('/tmp/claude-0/-home-user-Gonema/e3b3031e-119a-5939-a129-16b173290428/scratchpad/jobs.json','w'))
subprocess.run(['node',os.path.join(os.path.dirname(os.path.abspath(__file__)),'render.js'),'/tmp/claude-0/-home-user-Gonema/e3b3031e-119a-5939-a129-16b173290428/scratchpad/jobs.json'],check=True)
print('\n'.join(j['out'] for j in jobs))
