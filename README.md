# Klibana

Klibana aims to be a way to generate CSV files from elasticsearch. This can be done on modern ELK stacks by using the [watcher feature](http://watcher), but this feature is not available in old elasticsearch and in modern ones, it is a X-Pack feature (i.e. paid feature)

To solve this `klibana` can use a template file and a jq filter to drop a CSV output able to be processed by other tool. It's an automated way of getting something like kibana's table visualizations in CSV format w/o using the UI.

It is tested against the 7.1 and the 2.4 version of ES, but should work for any, as this does not use a specific library for ES queries.

## Requirements

`klibana` requires `jq` [installed in the system](https://github.com/stedolan/jq/wiki/Installation). Also `klibana` does not support ES authentication, but if you have it enabled most likely you also have watcher.

## Configuration

By default `klibana` will connect to elasticsearch in `http://localhost:9200`. This can be overridden using parameters, or the `KLIBANA_HOST` and `KLIBANA_PORT` environment variables. Also these variables can be configured in a `.env` file in the local directory or in a `$HOME/.klibanarc` file.

## Usage

```text
Usage of ./klibana:
  -debug
        Turns on debug log
  -es-result
        Stop and dump after getting ES result
  -host string
        Host for connecting elasticsearch
  -port string
        Port for connecting elasticsearch
  -query-file string
        File containing the elastic query and the processing instructions
  -time-window string
        redefined time windows, use --time-window help to see available settings

```

`--debug` is optional and will activate some debug to `stderr`, `--query-file` is mandatory as it is a json file where the instructions for ES and `jq` should be printed.

`--time-window` is optional but, if not present, the ES query cannot have variable time constraints, more on that later.

```text
klibana --time-window help

Possible values:

   today         Uses records from midnight to now
   yesterday     Uses records from yesterday at 0:00 to today at 0:00
   week          Uses recrods from last Sunday at 0:00 to now
   month         Uses records from day 1 of this month at 0:00 to now

All times are local, and weeks starts on Sunday
```

## Template Files

`klibana` requires a query file aimed to be reusable for periodic calls to the same ES query. It should be a json file with all the instructions for elasticsearch, CSV headers and `jq` processing filter.

```json
{
    "query_template": {"json":{"elastic":"query"}}},
    "query_headers": ["csv", "string", "headers"],
    "query_rows": ".any.valid[] | .jq | .\"filter\" | .as_string"
}
```

* `query_template`: A valid query for elasticsearch. It will be fired against the `logstash*/_search` endpoint. I've called it template because it can include a `###LTE###` and `###GTE###` tags which will be substituted for dynamically calculated time.
* `query_headers`: Must be an array of strings in valid json for the first row of the headers, as column titles aren't included in ES result.
* `query_rows` must be a `jq` filter used to convert the data returned by ES to comma separated values. The result output of jq will be processed as one row each line, cleaning any `[` or `]` in the process.

### LTE and GTE for time window

When you get a query against ES/Logstash you normally need a way to limit it to some interval. Also the interval boundaries may change on successive invocations of the tool.

This for example is done by this filter in kibana 4:

```json
"filter": {
    "bool": {
        "must": [{
            "range": {
            "@timestamp": {
                "gte": 1541026800000,
                "lte": 1541429712977,
                "format": "epoch_millis"
                }
            }
        }],
        "must_not": []
        }
    }
}

```

This epoch milis for the `timestamp` filter can be changed respectively by the marks `###GTE###` and `###LTE###` allowing to use the same template file for different interval aggregations or to reuse it every week without needing to recalculate the filter.

These tags will be changed in the ES query for they respective milliseconds after epoch, according to the `--time-window` specified.

Also, these tags and the use of `--time-window` parameter are optional but the tool will error if the tags are present in the query and there's no time window chosen.

### How to build ES Queries

First thing you must know to build the ES query is that it will be passed the same way to ES, after the `GTE`/`LTE` substitution, than if you do a `curl -XPOST http://host:port/logstash*/_search -d 'the json query'`.

To actually build the query You can learn about the query and aggregation json DSL in elasticsearch [query DSL](https://www.elastic.co/guide/en/elasticsearch/reference/current/query-dsl.html) and [aggregation DSL](https://www.elastic.co/guide/en/elasticsearch/reference/current/search-aggregations-bucket-terms-aggregation.html)

Also it is possible, at least for Kibana version 4 and version 7.1 to get the query by inspecting http traffic.

#### Example

Let's suppose we have data about the apache service and we want, for the first countries, the average and percentiles of the bytes download. We can build the table and the query in Kibana and see how it looks.

![kibana table](https://raw.githubusercontent.com/theist/klibana/img/img/klibana_table_sample.png)

I've added a filter and a query for reference.

With this table we can inspect the browser's internal using either Firefox web tools or the google chrome inspector.

Start the inspector (Ctrl + I in Chrome) and look for Network, XHR tab, and reload the table page. The inspector will show any request it makes.

![kibana XHR tab in chrome inspector](https://raw.githubusercontent.com/theist/klibana/img/img/klibana_XHR_sample.png)

The `msearch` ones will contain the payload sent to the underlying ES. This one has two XHR requests because the table originally had a "other" aggregation but kibana needs a second query to fil it. This is not currently possible with `klibana` as it will only process one single query.

![kibana payload sent](https://raw.githubusercontent.com/theist/klibana/img/img/klibana_headers_sample.png)

The `{"aggs"... }`  can be feed directly to klibana to get the same result from ES. To reuse it for other time frames it will require to overwrite the date filters with the `###LTE###`/`###GTE###` tags and switch the filter to `epoch_milis` to get something like this:

```text
{
    "query_template": {"aggs":{"2":{"terms":{"field":"geoip.country_name.keyword","order":{"3":"desc"},"missing":"__missing__","size":30},"aggs":{"3":{"avg":{"field":"bytes"}},"4":{"percentiles":{"field":"bytes","percents":[5,50,95],"keyed":false}}}}},"size":0,"_source":{"excludes":[]},"stored_fields":["*"],"script_fields":{},"docvalue_fields":[{"field":"@timestamp","format":"date_time"}],"query":{"bool":{"must":[{"range":{"@timestamp":{"format":"epoch_milis","gte":###GTE###,"lte":###LTE###}}}],"filter":[{"bool":{"should":[{"match":{"verb":"GET"}}],"minimum_should_match":1}},{"match_all":{}}],"should":[],"must_not":[{"match_phrase":{"tags":{"query":"_grokparsefailure"}}},{"match_phrase":{"tags":{"query":"_grokparsefailure"}}},{"match_phrase":{"tags":{"query":"_geoip_lookup_failure"}}}]}},"timeout":"30000ms"}
    ...
```

This method is also tested for ES 2.4 and Kibana 4, in fact those were the versions I needed it for, and the versions I tested against the most.

### How to build jq filter

Once you have the es query you need a filter to pass to jq to clean up the resulting json to return the rows of desired fields.

In order to do it `klibana` has a parameter to just dump the ES result. After getting it you can store it on a file and make the same call that `klibana` will do:

```text
jq -c <filter string> <es_data_file>
```

Every "array" line in the output will be stripped from the `[`, `]` and the double quote characters.

For json returned in the example above, this `jq` expression:

```text
'.aggregations."2".buckets[] | [ .key, .doc_count, ."3".value, ."4".values[].value ]"
```

Will return something like:

```text
["Belarus", "2", "341288981", "217.00000000000003", "341288981", "682577745"]
["Belgium", "6", "267549864.66666666", "32942840", "79228792", "682514126"]
["Iran", "2", "263180792", "4358144", "263180792", "522003440"]
["North Macedonia", "1", "261811464", "261811464", "261811464", "261811464"]
["Republic of Lithuania", "1", "248381440", "248381440", "248381440", "248381440"]
...
```

This will be converted to CSV data and added to the headers defined. Any `"` in the jq expression must be escaped as `\"` in the json template file

### Full example

```json
{
    "query_template": {"aggs":{"2":{"terms":{"field":"geoip.country_name.keyword","order":{"3":"desc"},"missing":"__missing__","size":30},"aggs":{"3":{"avg":{"field":"bytes"}},"4":{"percentiles":{"field":"bytes","percents":[5,50,95],"keyed":false}}}}},"size":0,"_source":{"excludes":[]},"stored_fields":["*"],"script_fields":{},"docvalue_fields":[{"field":"@timestamp","format":"date_time"}],"query":{"bool":{"must":[{"range":{"@timestamp":{"format":"epoch_millis","gte":"###GTE###","lte":"###LTE###"}}}],"filter":[{"bool":{"should":[{"match":{"verb":"GET"}}],"minimum_should_match":1}},{"match_all":{}}],"should":[],"must_not":[{"match_phrase":{"tags":{"query":"_grokparsefailure"}}},{"match_phrase":{"tags":{"query":"_grokparsefailure"}}},{"match_phrase":{"tags":{"query":"_geoip_lookup_failure"}}}]}},"timeout":"30000ms"}
    "query_headers": ["Country","number","average","5th percentile of bytes","50th percentile of bytes","95th percentile of bytes"]
    "query_rows": ".aggregations.\"2\".buckets[] | [ .key, .doc_count, .\"3\".value, .\"4\".values[].value ]"
}

```

---

## TODO

* Use a library instead of calling `jq` binary.
* Use a query/aggregation DSL for elastic instead the raw query.
* More time windows.
* Configurable index pattern (it assumes `logstash*` atm).
* Make `query_headers` not mandatory, to return headless CSVs, good for append.
* Make time window filter mode selectable, it only supports `epoch_milis` mode.
