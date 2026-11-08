package extract

import (
	"net/url"
	"strings"
	"testing"
)

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

const shopPage = `<!doctype html>
<html><head><title> Ada's  Salon &amp; Spa </title>
<style>body { color: red }</style><script>var x = "<p>not text</p>";</script></head>
<body>
<header class="site"><a href="/">Home</a> Call us now!</header>
<nav><a href="/services">Services</a> <a href="/about#team">About</a></nav>
<div hidden>hidden attribute</div>
<div aria-hidden="true">aria hidden</div>
<p style="display: none">inline hidden</p>
<p>Before main (left out when there is a main)</p>
<main>
  <h1>Welcome to   Ada's Salon</h1>
  <p>We do braids, weaves<br>and nails &mdash; walk-ins welcome.</p>
  <ul><li>Open <b>Monday</b> to Saturday</li><li>9am&nbsp;to&nbsp;7pm</li></ul>
  <table>
    <tr><th>Service</th><th>Price</th></tr>
    <tr><td>Box braids</td><td>&#8358;15,000</td></tr>
    <tr><td></td><td></td></tr>
  </table>
  <article><header><h2>News</h2></header><p>New branch in Yaba.</p></article>
  <form><label>Your email</label><input name="e"><button>Subscribe</button></form>
  <noscript>Enable JavaScript</noscript>
  <svg><text>svg text</text></svg>
  <template><p>template text</p></template>
  <a href="/services">Services</a>
  <a href="https://ada.example/prices?x=1#top">Prices</a>
  <a href="http://ada.example/insecure">Other scheme</a>
  <a href="https://ada.example:8443/other-port">Other port</a>
  <a href="https://ada.example:443/explicit-port">Explicit port</a>
  <a href="https://elsewhere.example/">Elsewhere</a>
  <a href="mailto:ada@example.com">Email</a>
  <a href="javascript:void(0)">JS</a>
  <a href="#top">Top</a>
</main>
<footer>Copyright Ada's Salon <a href="/privacy">Privacy</a></footer>
</body></html>`

func TestHTMLText(t *testing.T) {
	page, links, err := HTMLText([]byte(shopPage), mustURL(t, "https://ada.example/home"))
	if err != nil {
		t.Fatal(err)
	}
	if page.Title != "Ada's Salon & Spa" {
		t.Errorf("title = %q", page.Title)
	}
	if page.URL != "https://ada.example/home" {
		t.Errorf("url = %q", page.URL)
	}
	want := strings.Join([]string{
		"Welcome to Ada's Salon",
		"We do braids, weaves",
		"and nails — walk-ins welcome.",
		"Open Monday to Saturday",
		"9am to 7pm",
		"Service | Price",
		"Box braids | ₦15,000",
		"News",
		"New branch in Yaba.",
		"Services Prices Other scheme Other port Explicit port Elsewhere Email JS Top",
	}, "\n")
	if page.Text != want {
		t.Errorf("text:\n%s\nwant\n%s", page.Text, want)
	}
	var got []string
	for _, l := range links {
		got = append(got, l.String())
	}
	wantLinks := []string{
		"https://ada.example/",
		"https://ada.example/services",
		"https://ada.example/about",
		"https://ada.example/prices?x=1",
		"https://ada.example/explicit-port",
		"https://ada.example/home",
		"https://ada.example/privacy",
	}
	if strings.Join(got, "\n") != strings.Join(wantLinks, "\n") {
		t.Errorf("links:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(wantLinks, "\n"))
	}
}

func TestHTMLTextFallbacks(t *testing.T) {
	// No title: the first h1. No main: the articles. Relative links follow
	// <base href>.
	doc := `<html><head><base href="https://ada.example/blog/"></head><body>
<nav>Menu</nav><div>Sidebar text</div>
<article><h1>Hair care tips</h1><p>Oil your scalp weekly.</p></article>
<article><p>Second article.</p><a href="post-2">Next</a></article></body></html>`
	page, links, err := HTMLText([]byte(doc), mustURL(t, "https://ada.example/"))
	if err != nil {
		t.Fatal(err)
	}
	if page.Title != "Hair care tips" {
		t.Errorf("title = %q", page.Title)
	}
	if page.Text != "Hair care tips\nOil your scalp weekly.\nSecond article.\nNext" {
		t.Errorf("text = %q", page.Text)
	}
	if len(links) != 1 || links[0].String() != "https://ada.example/blog/post-2" {
		t.Errorf("links = %v", links)
	}

	// Neither: the whole body, chrome dropped.
	doc = `<body><header>Logo</header><div><p>Plain page.</p> Loose text <span>inline</span></div><footer>Foot</footer></body>`
	page, _, err = HTMLText([]byte(doc), mustURL(t, "http://ada.example"))
	if err != nil {
		t.Fatal(err)
	}
	if page.Title != "" || page.Text != "Plain page.\nLoose text inline" {
		t.Errorf("page = %+v", page)
	}

	// Preformatted text keeps its lines.
	page, _, _ = HTMLText([]byte("<main><pre>line one\n  line two</pre></main>"), mustURL(t, "http://ada.example"))
	if page.Text != "line one\nline two" {
		t.Errorf("pre = %q", page.Text)
	}
}
