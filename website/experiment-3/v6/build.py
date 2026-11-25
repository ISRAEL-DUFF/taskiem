"""Builds v6: index.html (the home page, a fragment for the artifact viewer) and
schools.html (a full document), sharing the home page's styles, header logos and footer."""
import re
import os
os.chdir(os.path.dirname(os.path.abspath(__file__)))
B = '../../../docs/brand/relay/'
home = open('home-src.html').read()
logos = {k: open(B + f).read().strip() for k, f in [('MARK', 'mark.svg'), ('WORDREV', 'wordmark-reversed.svg'), ('WORD', 'wordmark.svg')]}

def fill(s):
    for k in ('MARK', 'WORDREV', 'WORD'):
        s = s.replace('{{' + k + '}}', logos[k])
    return s

index = fill(home)
open('index.html', 'w').write(index)

css = re.search(r'<style>\n(.*?)\n</style>', home, re.S).group(1)
footer = re.search(r'<footer class="foot">.*?</footer>', index, re.S).group(0)
ring = re.search(r'<svg class="pr".*?</svg>', home, re.S).group(0)
rails = ''.join(re.search(rf'<span class="lg" title="{n}"><img [^>]*></span>', home).group(0) for n in ('Moniepoint', 'Flutterwave', 'Paystack'))

ICONS = {
 'clock': '<path d="M12 7v5l3 2"/><circle cx="12" cy="12" r="8"/>',
 'table': '<rect x="4" y="5" width="16" height="14" rx="2"/><path d="M4 10h16M10 10v9"/>',
 'invoice': '<path d="M6 3h12v18l-3-2-3 2-3-2-3 2z"/><path d="M9 8h6M9 12h6M9 16h3"/>',
 'loop': '<path d="M17 4l3 3-3 3"/><path d="M4 12v-1a4 4 0 0 1 4-4h12"/><path d="M7 20l-3-3 3-3"/><path d="M20 12v1a4 4 0 0 1-4 4H4"/>',
 'sms': '<path d="M5 5h14v10H10l-5 4z"/><path d="M9 9h6M9 12h4"/>',
 'bell': '<path d="M6 16V11a6 6 0 0 1 12 0v5l1.5 2h-15z"/><path d="M10 20a2 2 0 0 0 4 0"/>',
}
NODES = [
 (1, 1, 'clock', 'When', 'First day of term', 'Monday 7 September, 07:00', 'Ran 07:00'),
 (2, 1, 'table', 'Read', 'Classes and fees', "each class's fees, each family's options", '14 classes'),
 (3, 1, 'invoice', 'Invoice', 'Every enrolled student', 'once each, even if it runs again', '412 invoiced'),
 (3, 2, 'loop', 'Repeat', 'For each family', 'brothers and sisters together', '268 families'),
 (2, 2, 'sms', 'Send', 'Invoice and fees page', 'by WhatsApp, or SMS', '268 sent ✓'),
 (1, 2, 'bell', 'Tell', 'Message the bursar', 'what went out, and to whom', 'Report sent'),
]
nodes = "\n".join(
  f'            <div class="node" style="--c:{c};--r:{r}"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">{ICONS[i]}</svg></span><span class="k">{k}</span><b>{t}</b><small>{sub}</small><span class="res">{res}</span></div>'
  for c, r, i, k, t, sub, res in NODES)

schools = open('schools-src.html').read()
for k, v in [('BASECSS', css), ('FOOTER', footer), ('RING', ring), ('RAILS', rails), ('NODES', nodes)]:
    schools = schools.replace('{{' + k + '}}', v)
schools = fill(schools)
pricing = open('pricing-src.html').read()
for k, v in [('BASECSS', css), ('FOOTER', footer)]:
    pricing = pricing.replace('{{' + k + '}}', v)
pricing = fill(pricing)
assert '{{' not in schools and '{{' not in index and '{{' not in pricing, 'unfilled placeholder'
open('schools.html', 'w').write(schools)
open('pricing.html', 'w').write(pricing)
print('index', len(index), 'schools', len(schools), 'pricing', len(pricing))
