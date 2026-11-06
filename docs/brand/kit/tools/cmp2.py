import marks
from outline import outline
F='/tmp/claude-0/-home-user-Gonema/e3b3031e-119a-5939-a129-16b173290428/scratchpad/fonts/'
body=''
opts=[((36,16),'600'),((36,22),'600'),((34,24),'600'),((36,22),'500')]
for i,(st,g) in enumerate(opts):
    marks.A_STROKES[0]=st
    p,(x0,y0,x1,y1)=outline(F+f'ai-125-{g}.ttf','GONEMA',96,tracking=0.08)
    body+=f'<g transform="translate(0 {i*280})"><rect width="1300" height="270" fill="#F3EDE3"/>{marks.a_mark("#1C1713","#3B3FD6")}<g transform="translate({290-x0} {166-y0/2})"><path fill="#1C1713" d="{p}"/></g></g>'
open('../look/cmp2.svg','w').write(f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1300 {len(opts)*280}">{body}</svg>')
