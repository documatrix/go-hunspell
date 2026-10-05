// driver.cxx runs the C++ parsers of Hunspell (src/parsers) on the cases of
// this folder and prints what they do; the Go parsers are compared with its
// output. Build it with
//
//   P=$H/src/parsers
//   g++ -I$H/src/hunspell -I$P -I<folder of config.h> driver.cxx \
//       $P/textparser.cxx $P/firstparser.cxx $P/htmlparser.cxx \
//       $P/latexparser.cxx $P/manparser.cxx $P/xmlparser.cxx \
//       $P/odfparser.cxx $H/src/hunspell/csutil.cxx -o driver
//
// and run it as "driver NAME.in > NAME.out".
#include <cstdio>
#include <cstring>
#include <string>
#include <vector>
#include <algorithm>
#include "textparser.hxx"
#include "firstparser.hxx"
#include "htmlparser.hxx"
#include "latexparser.hxx"
#include "manparser.hxx"
#include "xmlparser.hxx"
#include "odfparser.hxx"
#include "csutil.hxx"

// unescape decodes \\ and \xHH.
static std::string unescape(const std::string& s) {
  std::string r;
  for (size_t i = 0; i < s.size(); i++) {
    if (s[i] == '\\' && i + 1 < s.size() && s[i + 1] == '\\') {
      r += '\\';
      i++;
    } else if (s[i] == '\\' && i + 3 < s.size() && s[i + 1] == 'x') {
      r += (char)strtol(s.substr(i + 2, 2).c_str(), nullptr, 16);
      i += 3;
    } else
      r += s[i];
  }
  return r;
}

// escape shows the bytes of s that are not printable ASCII as \xHH.
static std::string escape(const std::string& s) {
  std::string r;
  char b[8];
  for (unsigned char c : s) {
    if (c == '\\')
      r += "\\\\";
    else if (c < 32 || c >= 127) {
      snprintf(b, sizeof(b), "\\x%02x", c);
      r += b;
    } else
      r += (char)c;
  }
  return r;
}

int main(int argc, char** argv) {
  if (argc < 2)
    return 1;
  FILE* f = fopen(argv[1], "r");
  if (!f)
    return 1;
  std::string kind, wordchars;
  bool has_wordchars = false;
  int url = 0;
  std::vector<std::pair<std::string, std::string> > changes;
  std::vector<std::string> lines;
  char buf[65536];
  bool body = false;
  while (fgets(buf, sizeof(buf), f)) {
    std::string l(buf);
    if (!l.empty() && l[l.size() - 1] == '\n')
      l.resize(l.size() - 1);
    if (body) {
      lines.push_back(unescape(l));
      continue;
    }
    if (l == "---") {
      body = true;
    } else if (l.compare(0, 5, "kind ") == 0) {
      kind = l.substr(5);
    } else if (l.compare(0, 10, "wordchars ") == 0) {
      wordchars = unescape(l.substr(10));
      has_wordchars = true;
    } else if (l == "url") {
      url = 1;
    } else if (l.compare(0, 7, "change ") == 0) {
      std::string rest = l.substr(7);
      size_t sp = rest.find(' ');
      changes.push_back(std::make_pair(unescape(rest.substr(0, sp)), unescape(rest.substr(sp + 1))));
    }
  }
  fclose(f);
  std::vector<w_char> wc;
  if (has_wordchars)
    u8_u16(wc, wordchars);
  std::sort(wc.begin(), wc.end());
  const w_char* w = wc.empty() ? nullptr : wc.data();
  const char* w8 = wordchars.c_str();
  TextParser* p = nullptr;
  if (kind == "text") p = new TextParser(w8);
  else if (kind == "text-utf8") p = new TextParser(w, wc.size());
  else if (kind == "first") p = new FirstParser(w8);
  else if (kind == "latex") p = new LaTeXParser(w8);
  else if (kind == "latex-utf8") p = new LaTeXParser(w, wc.size());
  else if (kind == "html") p = new HTMLParser(w8);
  else if (kind == "html-utf8") p = new HTMLParser(w, wc.size());
  else if (kind == "xml") p = new XMLParser(w8);
  else if (kind == "xml-utf8") p = new XMLParser(w, wc.size());
  else if (kind == "odf") p = new ODFParser(w8);
  else if (kind == "odf-utf8") p = new ODFParser(w, wc.size());
  else if (kind == "man") p = new ManParser(w8);
  else if (kind == "man-utf8") p = new ManParser(w, wc.size());
  if (!p)
    return 1;
  p->set_url_checking(url);
  printf("utf8 %d\n", p->is_utf8());
  for (const auto& line : lines) {
    p->put_line(line.c_str());
    printf("line [%s]\n", escape(p->get_line()).c_str());
    std::string tok;
    while (p->next_token(tok)) {
      printf("token %d [%s] word [%s]\n", (int)p->get_tokenpos(), escape(tok).c_str(),
             escape(p->get_word(tok)).c_str());
      for (const auto& c : changes) {
        if (c.first == tok) {
          p->change_token(c.second.c_str());
          printf("change [%s]\n", escape(p->get_line()).c_str());
          bool ok = p->next_token(tok);
          printf("skip %d [%s]\n", ok, ok ? escape(tok).c_str() : "");
          break;
        }
      }
    }
  }
  for (int i = 0; i < MAXPREVLINE; i++)
    printf("prev %d [%s]\n", i, escape(p->get_prevline(i)).c_str());
  delete p;
  return 0;
}
