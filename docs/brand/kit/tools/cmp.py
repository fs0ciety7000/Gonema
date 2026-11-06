import marks
from marks import a_mark, f
from outline import outline
F='/tmp/claude-0/-home-user-Gonema/e3b3031e-119a-5939-a129-16b173290428/scratchpad/fonts/'
rows=[]; y=0
opts=[('112','600','Gonema',150,0.0),('112','600','GONEMA',104,0.06),('125','500','GONEMA',96,0.08),('125','600','GONEMA',96,0.08),('100','600','Gonema',150,-0.01),('125','500','Gonema',140,0.0)]
body=''
for i,(w,g,t,s,tr) in enumerate(opts):
    p,(x0,y0,x1,y1)=outline(F+f'ai-{w}-{g}.ttf',t,s,tracking=tr)
    capc=-y0/2
    body+=f'<g transform="translate(0 {i*280})"><rect width="1400" height="270" fill="#12100E"/>{a_mark("#F2E9DD","#8F96FF")}<g transform="translate({300-x0} {166+capc})"><path fill="#F2E9DD" d="{p}"/></g></g>'
open('../look/cmp.svg','w').write(f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1400 {len(opts)*280}">{body}</svg>')
